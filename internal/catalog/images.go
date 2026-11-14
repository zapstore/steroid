package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zapstore/relay/pkg/catalog"
	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/steroid/internal/run"
)

// EnrichImages fetches the icon and the author avatar. An empty filter is
// every android-arm64-v8a listing. Otherwise the app id must contain filter.
// The icon comes from the APK, so a listing with no APK gets none. The
// avatar comes from the author's kind 0 picture URL. Clone, scan, about, security, and the vector
// are left alone, including their cache columns.
func EnrichImages(ctx context.Context, data, dbPath, filter string, debug bool) error {
	filter = strings.TrimSpace(filter)
	listings, pictures, _, err := catalog.Apps(ctx, dbPath, filter)
	if err != nil {
		return err
	}
	listings = androidListings(listings)
	if len(listings) == 0 {
		if filter == "" {
			return fmt.Errorf("no android-arm64-v8a listings")
		}
		return fmt.Errorf("no android-arm64-v8a listings match %q", filter)
	}
	db, err := artifacts.Open(ctx, artifacts.Path(data))
	if err != nil {
		return err
	}
	defer db.Close()
	line := fmt.Sprintf("images %d", len(listings))
	if filter != "" {
		line += ", filter " + filter
	}
	writeBlock(ctx, line)
	var avatars sync.Mutex
	var failed atomic.Int32
	err = processListings(ctx, listings, parallelJobs, func(ctx context.Context, _ int, listing Listing) error {
		bad, runErr := imagesOne(ctx, db, data, listing, pictures[listing.App.PubKey], &avatars, debug)
		if bad || runErr != nil {
			failed.Add(1)
		}
		return nil
	})
	writeBlock(ctx, summaryLine(len(listings), 0, int(failed.Load())))
	return err
}

// imagesOne fetches one listing's icon and avatar. bad is a fetch failure,
// which the next run retries. err is a database failure.
func imagesOne(ctx context.Context, db *artifacts.DB, data string, listing Listing, pictureURL string, avatars *sync.Mutex, debug bool) (bool, error) {
	rep := &appReport{id: listing.AppID, live: debug, ctx: ctx}
	defer rep.flush()
	in, _, err := ListingInput(listing, "")
	if err != nil {
		rep.fail("listing", err)
		return true, err
	}
	rep.version = in.Version
	stored, err := db.Load(ctx, listing.AppID)
	if err != nil {
		rep.fail("db", err)
		return true, err
	}
	from, note, err := fetchIcon(ctx, db, data, in, stored)
	switch {
	case err != nil:
		rep.fail("icon", err)
	case from != "":
		if err := rememberIcon(ctx, db, listing.AppID, stored, from); err != nil {
			rep.fail("db", err)
			return true, err
		}
		if from == iconNone {
			rep.degrade("icon", note)
		} else {
			rep.mark("icon")
		}
	}
	wrote, err := storeAvatar(ctx, db, avatars, listing.App.PubKey, pictureURL)
	if err != nil {
		rep.degrade("avatar", err.Error())
	} else if wrote {
		rep.mark("avatar")
	}
	if !rep.failed() && !rep.isDegraded() && len(rep.ok) == 0 {
		rep.quiet = true
	}
	return rep.failed(), nil
}

// fetchIcon reads the icon from the APK, then from the listing's icon URL
// when the APK has none. It returns where the icon came from, or iconNone
// and the reason when neither has one. "" means nothing was done: the listing
// has no APK, or the question is already settled. An APK that cannot be read
// is an error.
func fetchIcon(ctx context.Context, db *artifacts.DB, data string, in run.Input, stored artifacts.App) (from, note string, err error) {
	has := len(stored.Icon) > 0
	// settled is an icon this memo already explains. It is trusted only for
	// the APK hash it came from: a new release can change the launcher icon,
	// and rememberIcon does not advance Memo.APK, so a hash that still
	// matches means the full enrich pipeline scanned this exact APK too.
	settled := (stored.Memo.Icon == iconAPK || stored.Memo.Icon == iconListing) && stored.Memo.APK == in.APKHash
	switch {
	case strings.TrimSpace(in.APKHash) == "":
		return "", "", nil
	case has && settled:
		return "", "", nil
	case !has && stored.Memo.Icon == iconNone:
		return "", "", nil
	}
	webp, err := iconFromListingAPK(ctx, data, in)
	if err != nil {
		return "", "", err
	}
	from = iconAPK
	if len(webp) == 0 {
		from = iconListing
		if strings.TrimSpace(in.IconURL) == "" {
			return iconNone, "APK has no icon, listing has none", nil
		}
		if webp, err = iconFromURL(ctx, in.IconURL); err != nil {
			return iconNone, "APK has no icon, listing URL failed: " + err.Error(), nil
		}
	}
	if err := storeIcon(ctx, db, in.AppID, stored.Icon, webp); err != nil {
		return "", "", err
	}
	return from, "", nil
}

func iconFromListingAPK(ctx context.Context, data string, in run.Input) ([]byte, error) {
	path, err := tempAPK()
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	file, err := openAPK(ctx, data, path, in)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return iconFromAPK(ctx, file.Path)
}

// rememberIcon records the icon source and leaves the other cache columns as
// they were. A row that has no cache yet is stamped with this enrich version
// so a later full run skips the icon and still does the other stages.
func rememberIcon(ctx context.Context, db *artifacts.DB, id string, stored artifacts.App, from string) error {
	m := memoFrom(stored.Memo)
	if m.Icon == from && m.Version != 0 {
		return nil
	}
	if m.Version == 0 && m.APK == "" && m.Feature == "" && m.Facts == "" && m.About == "" {
		m.Version = enrichVersion
	}
	m.Icon = from
	return db.SetMemo(ctx, id, memoTo(m))
}

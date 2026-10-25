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
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/run"
)

// EnrichImages fetches the icon and the author avatar. An empty filter is
// every android-arm64-v8a listing. Otherwise the app id must contain filter.
// The icon comes from the listing URL, then from the APK when that URL is
// missing or fails and the listing has an APK. The avatar comes from the
// author's kind 0 picture URL. Clone, scan, about, security, and the vector
// are left alone, including their cache columns.
func EnrichImages(ctx context.Context, data, dbPath, filter string, debug bool) error {
	filter = strings.TrimSpace(filter)
	listings, pictures, err := catalog.Apps(ctx, dbPath, filter)
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
	got, err := fetchIcon(ctx, db, data, in, stored)
	if err != nil {
		rep.fail("icon", err)
	} else if got.from != "" {
		if err := rememberIcon(ctx, db, listing.AppID, stored, got.from); err != nil {
			rep.fail("db", err)
			return true, err
		}
		if got.fetched {
			if got.fallback != "" {
				rep.degrade("icon", "listing URL failed, used the APK")
			} else {
				rep.mark("icon")
			}
		}
	}
	wrote, err := storeAvatar(ctx, db, avatars, listing.App.PubKey, pictureURL)
	if err != nil {
		rep.fail("avatar", err)
	} else if wrote {
		rep.mark("avatar")
	}
	if !rep.failed() && !rep.isDegraded() && len(rep.ok) == 0 {
		rep.quiet = true
	}
	return rep.failed(), nil
}

// iconFetch is where the stored icon came from. fetched is false when the
// stored bytes already matched. fallback is set when the listing URL failed
// and the APK icon was stored instead.
type iconFetch struct {
	from     string
	fetched  bool
	fallback string
}

// fetchIcon tries the listing icon URL, then the APK. A database error is
// returned as-is. A missing URL and a missing APK stores nothing.
func fetchIcon(ctx context.Context, db *artifacts.DB, data string, in run.Input, stored artifacts.App) (iconFetch, error) {
	url := strings.TrimSpace(in.IconURL)
	apkHash := strings.TrimSpace(in.APKHash)
	has := len(stored.Icon) > 0
	if url != "" && has && stored.Memo.Icon == url {
		return iconFetch{from: url}, nil
	}
	if url == "" && apkHash != "" && has && stored.Memo.Icon == "apk" {
		return iconFetch{from: "apk"}, nil
	}
	var urlErr error
	if url != "" {
		err := writeIconURL(ctx, db, in.AppID, stored.Icon, url)
		if err == nil {
			return iconFetch{from: url, fetched: true}, nil
		}
		urlErr = err
	}
	if apkHash == "" {
		return iconFetch{}, urlErr
	}
	webp, err := iconFromListingAPK(ctx, data, in)
	if err != nil || len(webp) == 0 {
		if err == nil {
			err = fmt.Errorf("apk has no icon")
		}
		if urlErr != nil {
			return iconFetch{}, fmt.Errorf("%w; apk: %v", urlErr, err)
		}
		return iconFetch{}, err
	}
	if err := storeIcon(ctx, db, in.AppID, stored.Icon, webp); err != nil {
		return iconFetch{}, err
	}
	got := iconFetch{from: "apk", fetched: true}
	if urlErr != nil {
		got.fallback = urlErr.Error()
	}
	return got, nil
}

func writeIconURL(ctx context.Context, db *artifacts.DB, id string, prev []byte, url string) error {
	raw, err := picture.Fetch(ctx, url)
	if err != nil {
		return err
	}
	webp, err := picture.Encode(raw, picture.Icon)
	if err != nil {
		return err
	}
	if len(webp) == 0 {
		return fmt.Errorf("empty icon")
	}
	return storeIcon(ctx, db, id, prev, webp)
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

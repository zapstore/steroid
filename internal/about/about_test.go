package about

import (
	"strings"
	"testing"
)

func TestEdit(t *testing.T) {
	tests := []struct {
		name, in, want string
		ok             bool
	}{
		{
			name: "Amber",
			in:   "Amber is a nostr event signer for Android. It allows users to keep their nsec segregated in a single, dedicated app, so Amber can sign for web clients too.",
			want: "Nostr event signer for Android. It allows users to keep their nsec segregated in a single, dedicated app, so the app can sign for web clients too.",
			ok:   true,
		},
		{
			name: "Notes",
			in:   "Notes lets you write and organize notes in folders, pin the ones you need, and find any of them with full text search. A free and open source app. Also on F-Droid.",
			want: "Lets you write and organize notes in folders, pin the ones you need, and find any of them with full text search.",
			ok:   true,
		},
		{name: "Tiny", in: "Tiny: a calculator.", want: "A calculator.", ok: false},
		{
			name: "Todo",
			in:   "一个简洁的待办事项应用，支持分类、提醒和深色模式，完全离线使用，数据只保存在手机上，没有广告，也没有任何追踪，适合每天记录待办事项和计划的人使用，界面简洁清爽，操作方便快捷，支持小组件和多种主题颜色。",
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Edit("", tt.name, tt.in)
			if ok != tt.ok || (tt.want != "" && got != tt.want) {
				t.Errorf("Edit(%q) = %q, %v, want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDescriptionEditsTheListingText(t *testing.T) {
	got := Description("", "Amber", "Amber is a nostr event signer for Android. It allows users to keep their nsec segregated in a single, dedicated app.")
	if got != "Nostr event signer for Android. It allows users to keep their nsec segregated in a single, dedicated app." {
		t.Fatalf("%q", got)
	}
	if got := Description("", "Amber", "Signer"); got != "" {
		t.Fatalf("short text kept: %q", got)
	}
	if got := Description("", "Amber", ""); got != "" {
		t.Fatalf("empty kept: %q", got)
	}
}

func TestDescriptionKeepsListingTextThatIsAFeatureList(t *testing.T) {
	text := "A privacy-focused Nostr client for Android. Built-in TOR support, the most configurable relay system,\nencrypted messaging, zaps, marketplaces, live streams and complete data sovereignty."
	want := "A privacy-focused Nostr client for Android. Built-in TOR support, the most configurable relay system, encrypted messaging, zaps, marketplaces, live streams and complete data sovereignty."
	if got := Description("", "Amethyst", text); got != want {
		t.Fatalf("%q", got)
	}
}

func TestOthersDropsEmptyAndRepeated(t *testing.T) {
	got := Others([]Text{
		{Source: "listing summary", Body: ""},
		{Source: "readme", Body: "Signs events."},
		{Source: "website", Body: "signs   events."},
	})
	if len(got) != 1 || got[0].Source != "readme" {
		t.Fatalf("%+v", got)
	}
}

func TestVerify(t *testing.T) {
	corpus := "Amber is a nostr event signer for Android. It allows users to keep their nsec segregated in a single, dedicated app. Supports multiple accounts and NIP-46 remote signing. The goal of Amber is to have your smartphone act as a signing device without any need for servers or additional hardware. Private keys should be exposed to as few systems as possible, as each system adds to the attack surface. Approve or reject each request, remember permissions per client, and read the article explaining the protocol. Bunker connections, encrypted backups, profile switching, notification history, battery friendly background service, and a clean interface with dark themes are included."
	check := Verify(corpus)
	if check == nil {
		t.Fatal("corpus long enough to check")
	}
	if err := check("Nostr event signer that keeps your nsec in one dedicated app, with multiple accounts and NIP-46 remote signing. Read the articles."); err != nil {
		t.Fatal(err)
	}
	err := check("Blazing fast revolutionary wallet with military grade encryption and seamless cloud backup.")
	if err == nil || !strings.Contains(err.Error(), "revolutionary") {
		t.Fatalf("invented text accepted: %v", err)
	}
	if Verify("一个简洁的待办事项应用，支持分类、提醒和深色模式。") != nil {
		t.Fatal("non-English corpus should skip the check")
	}
	if Verify("Wikipedia. The free encyclopedia.") != nil {
		t.Fatal("short corpus should skip the check")
	}
}

func TestDescriptionOffersOpeningSentences(t *testing.T) {
	long := "Amber is a nostr event signer for Android. It allows users to keep their nsec segregated in a single, dedicated app. The goal of Amber is to have your smartphone act as a NIP-46 signing device without any need for servers or additional hardware. Private keys should be exposed to as few systems as possible, as each system adds to the attack surface, as the rationale of this project goes."
	want := "Nostr event signer for Android. It allows users to keep their nsec segregated in a single, dedicated app. The goal of the app is to have your smartphone act as a NIP-46 signing device without any need for servers or additional hardware."
	if got := Description("", "Amber", long); got != want {
		t.Fatalf("%q", got)
	}
}

func TestEditReadsAsEnglishAfterTheName(t *testing.T) {
	tests := []struct {
		id, name, in, want string
	}{
		{
			"com.sleepguard", "SleepGuard",
			"Do you often fall asleep while watching videos? SleepGuard makes sure your device doesn't keep running all night long.",
			"Do you often fall asleep while watching videos? The app makes sure your device doesn't keep running all night long.",
		},
		{
			"de.piratentools.spickerrr", "Spickerrr",
			"You don't want to have to constantly search for motions in the wiki at the party conference. Then the Spickerrr is just right for you.",
			"You don't want to have to constantly search for motions in the wiki at the party conference. Then the app is just right for you.",
		},
		{
			"org.fossify.musicplayer", "Music Player",
			"Introducing Fossify Music Player – your gateway to uninterrupted musical enjoyment, with offline playback and an equalizer. 🎧",
			"Introducing the app – your gateway to uninterrupted musical enjoyment, with offline playback and an equalizer.",
		},
		{
			"org.eclipse.kuksa.companion", "KUKSA Companion App",
			"The KUKSA Companion App demonstrates the capabilities of the Android SDK to connect to a vehicle with an integrated KUKSA Databroker.",
			"The app demonstrates the capabilities of the Android SDK to connect to a vehicle with an integrated KUKSA Databroker.",
		},
		{
			"xyz.zedler.patrick.doodle", "Doodle",
			"Doodle Android is an open-source Android app that provides colorful live wallpapers with auto dark mode and power-efficient animations.",
			"Android app that provides colorful live wallpapers with auto dark mode and power-efficient animations.",
		},
		{
			"be.robinj.distrohopper", "DistroHopper",
			"Are you a Linux/open-source enthusiast? If it seems cool to have the Linux desktop on your Android device, then this app is what you're looking for.",
			"Are you a Linux enthusiast? If it seems cool to have the Linux desktop on your Android device, then this app is what you're looking for.",
		},
		{
			"ml.docilealligator.infinityforreddit.plus", "Infinity",
			"Experience the fast, open-source Reddit app built to give you back control, so you can browse, post, and engage with your communities.",
			"Experience the fast Reddit app built to give you back control, so you can browse, post, and engage with your communities.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Edit(tt.id, tt.name, tt.in)
			if !ok || got != tt.want {
				t.Errorf("Edit(%q) = %q, %v\nwant %q", tt.in, got, ok, tt.want)
			}
		})
	}
}

func TestEditRejectsAnotherProductOnTheName(t *testing.T) {
	in := "Run a modern AI agent yourself without external services. You need just this app and a computer to run the Relagent Engine on your network."
	if got, ok := Edit("org.venkado.relagent", "Relagent", in); ok {
		t.Fatalf("kept %q", got)
	}
}

func TestEditDropsNameWithSoftHyphen(t *testing.T) {
	got, ok := Edit("", "Street\u00adComplete", "StreetComplete is an easy to use editor of OpenStreetMap data available for Android. It can be used without any OpenStreetMap-specific knowledge.")
	if !ok || got != "Easy to use editor of OpenStreetMap data available for Android. It can be used without any OpenStreetMap-specific knowledge." {
		t.Fatalf("%v %q", ok, got)
	}
}

package detect

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf16"

	"github.com/zapstore/steroid/internal/lang"
)

const maxArsc = 64 << 20

// Strings is the app's own string resources: the default values and the
// languages that translate them. Library strings are left out.
type Strings struct {
	Default map[string]string
	// Translated maps a language code to the app keys it translates.
	Translated map[string]map[string]bool
}

// libraryPrefix and libraryKey are string resources that ship with
// AndroidX, Material, Play services, and other common libraries.
var libraryPrefix = []string{
	"abc_", "mtrl_", "m3_", "m3c_", "material_", "androidx_", "exo_", "common_google_play_services_",
	"fcm_", "call_notification_", "bottomsheet_", "bottom_sheet_", "character_counter_",
	"clear_text_end_icon_", "error_icon_", "exposed_dropdown_menu_", "fab_transformation_",
	"hide_bottom_view_", "password_toggle_", "path_password_", "side_sheet_",
	"searchbar_", "searchview_", "fallback_menu_item_", "v7_preference_", "tooltip_",
	"nav_app_bar_", "gcm_", "firebase_", "leak_canary_", "com.", "androidx.", "bottom_navigation_",
	"time_picker_", "date_picker_", "date_range_picker_", "search_bar_", "snackbar_",
}

var libraryKey = map[string]bool{
	"icon_content_description": true, "item_view_role_description": true,
	"appbar_scrolling_view_behavior": true, "status_bar_notification_info_overflow": true,
	"search_menu_title": true, "preference_copied": true, "summary_collapsed_preference_list": true,
	"expand_button_title": true, "copy_toast_msg": true, "not_set": true, "copy": true,
	"default_web_client_id": true, "google_api_key": true, "google_app_id": true,
	"google_crash_reporting_api_key": true, "google_storage_bucket": true, "project_id": true,
	"close_drawer": true, "close_sheet": true, "default_popup_window_title": true,
	"dialog": true, "dropdown_menu": true, "in_progress": true, "indeterminate": true,
	"navigation_menu": true, "off": true, "on": true, "range_end": true, "range_start": true,
	"selected": true, "switch_role": true, "tab": true, "template_percent": true,
	"state_empty": true, "state_off": true, "state_on": true, "not_selected": true,
	"autofill": true, "chip_text": true,
}

// AppStrings reads resources.arsc from the APK.
func AppStrings(apkPath string) (Strings, error) {
	r, err := zip.OpenReader(apkPath)
	if err != nil {
		return Strings{}, err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != "resources.arsc" {
			continue
		}
		if f.UncompressedSize64 > maxArsc {
			return Strings{}, errors.New("resources.arsc too large")
		}
		rc, err := f.Open()
		if err != nil {
			return Strings{}, err
		}
		raw, err := io.ReadAll(io.LimitReader(rc, maxArsc))
		closeErr := rc.Close()
		if err != nil {
			return Strings{}, err
		}
		if closeErr != nil {
			return Strings{}, closeErr
		}
		return parseStrings(raw)
	}
	return Strings{}, nil
}

func appKey(key string) bool {
	if libraryKey[key] {
		return false
	}
	for _, p := range libraryPrefix {
		if strings.HasPrefix(key, p) {
			return false
		}
	}
	return true
}

type stringPool struct {
	data    []byte
	offsets []uint32
	start   uint32
	utf8    bool
}

func (p stringPool) get(i uint32) string {
	if int(i) >= len(p.offsets) {
		return ""
	}
	at := int(p.start + p.offsets[i])
	if at >= len(p.data) {
		return ""
	}
	b := p.data[at:]
	if p.utf8 {
		_, n := utf8Len(b)
		if n >= len(b) {
			return ""
		}
		size, m := utf8Len(b[n:])
		b = b[n+m:]
		if size > len(b) {
			return ""
		}
		return string(b[:size])
	}
	if len(b) < 2 {
		return ""
	}
	size := int(binary.LittleEndian.Uint16(b))
	b = b[2:]
	if size&0x8000 != 0 && len(b) >= 2 {
		size = (size&0x7fff)<<16 | int(binary.LittleEndian.Uint16(b))
		b = b[2:]
	}
	if size*2 > len(b) {
		return ""
	}
	u := make([]uint16, size)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

func utf8Len(b []byte) (int, int) {
	if len(b) == 0 {
		return 0, 1
	}
	if b[0]&0x80 == 0 {
		return int(b[0]), 1
	}
	if len(b) < 2 {
		return 0, 2
	}
	return int(b[0]&0x7f)<<8 | int(b[1]), 2
}

func readPool(chunk []byte) (stringPool, bool) {
	if len(chunk) < 28 || binary.LittleEndian.Uint16(chunk) != 0x0001 {
		return stringPool{}, false
	}
	hdr := binary.LittleEndian.Uint16(chunk[2:])
	count := binary.LittleEndian.Uint32(chunk[8:])
	flags := binary.LittleEndian.Uint32(chunk[16:])
	start := binary.LittleEndian.Uint32(chunk[20:])
	if int(hdr)+int(count)*4 > len(chunk) {
		return stringPool{}, false
	}
	offsets := make([]uint32, count)
	for i := range offsets {
		offsets[i] = binary.LittleEndian.Uint32(chunk[int(hdr)+i*4:])
	}
	return stringPool{data: chunk, offsets: offsets, start: start, utf8: flags&(1<<8) != 0}, true
}

// parseStrings walks the table, its global string pool, and each package's
// string type chunks. It reads only what the language check needs.
func parseStrings(raw []byte) (Strings, error) {
	out := Strings{Default: map[string]string{}, Translated: map[string]map[string]bool{}}
	if len(raw) < 12 || binary.LittleEndian.Uint16(raw) != 0x0002 {
		return out, errors.New("not a resource table")
	}
	var global stringPool
	at := int(binary.LittleEndian.Uint16(raw[2:]))
	for at+8 <= len(raw) {
		typ := binary.LittleEndian.Uint16(raw[at:])
		size := int(binary.LittleEndian.Uint32(raw[at+4:]))
		if size < 8 || at+size > len(raw) {
			break
		}
		chunk := raw[at : at+size]
		switch typ {
		case 0x0001:
			global, _ = readPool(chunk)
		case 0x0200:
			readPackage(chunk, global, &out)
		}
		at += size
	}
	return out, nil
}

func readPackage(pkg []byte, global stringPool, out *Strings) {
	if len(pkg) < 288 {
		return
	}
	hdr := int(binary.LittleEndian.Uint16(pkg[2:]))
	typeAt := int(binary.LittleEndian.Uint32(pkg[268:]))
	keyAt := int(binary.LittleEndian.Uint32(pkg[276:]))
	if typeAt >= len(pkg) || keyAt >= len(pkg) {
		return
	}
	types, ok := readPool(pkg[typeAt:])
	if !ok {
		return
	}
	keys, ok := readPool(pkg[keyAt:])
	if !ok {
		return
	}
	stringType := uint8(0)
	for i := range types.offsets {
		if types.get(uint32(i)) == "string" {
			stringType = uint8(i + 1)
		}
	}
	if stringType == 0 {
		return
	}
	at := hdr
	for at+8 <= len(pkg) {
		typ := binary.LittleEndian.Uint16(pkg[at:])
		size := int(binary.LittleEndian.Uint32(pkg[at+4:]))
		if size < 8 || at+size > len(pkg) {
			return
		}
		if typ == 0x0201 && size > 20 && pkg[at+8] == stringType {
			readType(pkg[at:at+size], global, keys, out)
		}
		at += size
	}
}

func readType(chunk []byte, global, keys stringPool, out *Strings) {
	hdr := int(binary.LittleEndian.Uint16(chunk[2:]))
	flags := chunk[9]
	count := int(binary.LittleEndian.Uint32(chunk[12:]))
	entries := int(binary.LittleEndian.Uint32(chunk[16:]))
	// ResTable_config starts at 20: size, mcc, mnc, then language[2].
	if hdr < 30 || len(chunk) < 30 {
		return
	}
	language := ""
	if chunk[28] != 0 && chunk[28]&0x80 == 0 {
		language = strings.ToLower(string(chunk[28:30]))
	} else if chunk[28] != 0 {
		language = "packed"
	}
	sparse := flags&0x01 != 0
	offset16 := flags&0x02 != 0
	width := 4
	if offset16 && !sparse {
		width = 2
	}
	for i := range count {
		pos := hdr + i*width
		if pos+width > len(chunk) {
			return
		}
		var off int
		switch {
		case sparse:
			off = int(binary.LittleEndian.Uint16(chunk[pos+2:])) * 4
		case offset16:
			v := binary.LittleEndian.Uint16(chunk[pos:])
			if v == 0xffff {
				continue
			}
			off = int(v) * 4
		default:
			v := binary.LittleEndian.Uint32(chunk[pos:])
			if v == 0xffffffff {
				continue
			}
			off = int(v)
		}
		e := entries + off
		if e+8 > len(chunk) {
			continue
		}
		eflags := binary.LittleEndian.Uint16(chunk[e+2:])
		var key uint32
		var dataType uint8
		var data uint32
		if eflags&0x08 != 0 {
			key = uint32(binary.LittleEndian.Uint16(chunk[e:]))
			dataType = uint8(eflags >> 8)
			data = binary.LittleEndian.Uint32(chunk[e+4:])
		} else {
			if eflags&0x01 != 0 || e+16 > len(chunk) {
				continue
			}
			key = binary.LittleEndian.Uint32(chunk[e+4:])
			dataType = chunk[e+11]
			data = binary.LittleEndian.Uint32(chunk[e+12:])
		}
		if dataType != 0x03 {
			continue
		}
		name := keys.get(key)
		if name == "" || !appKey(name) {
			continue
		}
		if language == "" {
			out.Default[name] = global.get(data)
			continue
		}
		if out.Translated[language] == nil {
			out.Translated[language] = map[string]bool{}
		}
		out.Translated[language][name] = true
	}
}

// English reports whether the app's own strings are English, or have an
// English translation for most of them. ok is false when there are too few
// app strings to decide, as in Flutter or React Native apps.
func (s Strings) English() (english, ok bool) {
	// Labels such as "Hill" carry no common words, so only phrases count.
	values := make([]string, 0, len(s.Default))
	for _, v := range s.Default {
		if len(strings.Fields(v)) >= 3 {
			values = append(values, v)
		}
	}
	isEnglish, decided := lang.UI(values)
	if !decided {
		return false, false
	}
	if isEnglish {
		return true, true
	}
	en := s.Translated["en"]
	return len(en)*2 >= len(s.Default), true
}

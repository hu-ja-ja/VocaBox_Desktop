package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	vocabox "github.com/hu-ja-ja/VocaBox_Desktop"
)

// TestNativeUIShowsLicenses checks the settings say what the app is, which
// licence it is under, and which works it bundles, whose terms open in the
// list.
func TestNativeUIShowsLicenses(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("設定"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("Copyright (C) 2026 hu-ja-ja") {
		t.Fatalf("the settings do not say the copyright: %q", tester.Texts())
	}
	if !tester.HasText("サードパーティライセンス") {
		t.Fatalf("the settings do not head the third-party licences: %q", tester.Texts())
	}
	// The icons the app takes from Lucide are one of the bundled works.
	if !tester.HasText("Lucide") {
		t.Fatalf("the icons are not listed: %q", tester.Texts())
	}
	// The app's own licence opens its text.
	if err := tester.Click("MPL-2.0 License (Mozilla Public License Version 2.0)を表示"); err != nil {
		t.Fatal(err)
	}
	if !app.licenseOpenDialog || !strings.Contains(app.licenseBody, "Mozilla Public License Version 2.0") {
		t.Fatalf("the app's licence did not open: open=%v", app.licenseOpenDialog)
	}
	if err := tester.Click("閉じる"); err != nil {
		t.Fatal(err)
	}
	if app.licenseOpenDialog {
		t.Fatal("the app's licence did not close")
	}

	// The about card opens the repository in the browser.
	if err := tester.Click("リポジトリ"); err != nil {
		t.Fatal(err)
	}
	if opened := tester.OpenedURLs(); len(opened) != 1 || opened[0] != appRepo {
		t.Fatalf("the repository did not open: %#v", opened)
	}

	// A bundled work shows as one row, and opens onto the text of its terms.
	var entry vocabox.Entry
	for _, e := range vocabox.Entries {
		if e.Name == "github.com/egoist/mygo" {
			entry = e
		}
	}
	if entry.Name == "" {
		t.Fatalf("mygo is not listed: %d entries", len(vocabox.Entries))
	}
	// The list names a Go module by the package it ends in; opening the row
	// spells the module out.
	if got := licenseTitle(entry); got != "mygo v0.7.4  ·  MIT" {
		t.Fatalf("the list names the module %q; want its package name", got)
	}
	tester.Scroll(560, 500, 0, 160)
	tester.Frame()
	if err := tester.Click(licenseTitle(entry)); err != nil {
		t.Fatal(err)
	}
	if !app.licenseOpen[entry.Name+" "+entry.Version] {
		t.Fatalf("the row of %s did not open: %#v", entry.Name, app.licenseOpen)
	}
	if !tester.HasText("github.com/egoist/mygo") {
		t.Fatalf("the open row does not spell the module out: %q", tester.Texts())
	}
	if !tester.HasText("Permission is hereby granted, free of charge") {
		t.Fatalf("the row does not show the terms: %q", tester.Texts())
	}
}

// TestNativeUITogglesOfflineSaving checks the mylist menu's オフライン保存 and
// the mylist page's own button: the flag is written either way, and the
// mylist reads as saved afterwards.
func TestNativeUITogglesOfflineSaving(t *testing.T) {
	app := savedApp(t)
	noAudioServer(app)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト"}, testItems("sm1"))
	tester := ui.NewTester(app.view, 1120, 760)

	if err := tester.RightClick("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("オフライン保存"); !ok {
		t.Fatalf("the menu has no offline saving: %q", tester.Texts())
	}
	if err := tester.Click("オフライン保存"); err != nil {
		t.Fatal(err)
	}
	index, err := app.mylist.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := indexAt(index, "111"); !ok || !index[at].Download {
		t.Fatalf("the menu did not turn offline saving on: %+v", index)
	}
	if entry := app.savedEntry("111"); entry == nil || !entry.Download {
		t.Fatal("the view does not know the mylist is saved")
	}

	// The mylist page says it is saved, and its button turns the saving off
	// again.
	if err := tester.Click("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("オフライン保存をやめる"); !ok {
		t.Fatalf("the page does not offer to stop: %q", tester.Texts())
	}
	if err := tester.Click("オフライン保存をやめる"); err != nil {
		t.Fatal(err)
	}
	index, err = app.mylist.ListSaved()
	if err != nil {
		t.Fatal(err)
	}
	if at, ok := indexAt(index, "111"); !ok || index[at].Download {
		t.Fatalf("the page's button did not turn offline saving off: %+v", index)
	}
}

// TestNativeUISettingsQuality checks the quality the settings dropdown keeps:
// the choice is stored in the form the downloads pick their band by, and a
// change asks for the saved audio to be fetched again.
func TestNativeUISettingsQuality(t *testing.T) {
	app := newNativeApp(NewMylist())
	tester := ui.NewTester(app.view, 1120, 760)
	if err := tester.Click("設定"); err != nil {
		t.Fatal(err)
	}
	// The dropdown shows the current preset, and opening it lists every
	// preset, the estimates included.
	if !tester.HasText(audioQualityLabel(320)) {
		t.Fatalf("the settings show no quality choice: %q", tester.Texts())
	}
	if err := tester.Click(audioQualityLabel(320)); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText(audioQualityLabel(64)) {
		t.Fatalf("the dropdown lists no low preset: %q", tester.Texts())
	}
	if err := tester.Click(audioQualityLabel(64)); err != nil {
		t.Fatal(err)
	}
	if app.audioKbps.Load() != 64 || app.qualityOption != audioQualityLabel(64) {
		t.Fatalf("the choice did not reach the downloads: %d/%q", app.audioKbps.Load(), app.qualityOption)
	}
	if got := audioQualityValue(int(app.audioKbps.Load())); got != "64" {
		t.Fatalf("the stored quality = %q; want the low preset", got)
	}
}

// TestNativeUIShowsOfflineSavingChip checks that a download-enabled mylist
// carries its saving state — the saved count included — on its card and on
// its page.
func TestNativeUIShowsOfflineSavingChip(t *testing.T) {
	app := savedApp(t)
	noAudioServer(app)
	indexSaved(t, app, SavedIndexEntry{ID: "111", Name: "朝のリスト"}, testItems("sm1", "sm2", "sm3"))
	saved, err := app.mylist.SetDownload("111", true)
	if err != nil {
		t.Fatal(err)
	}
	app.saved = saved
	app.audioStats["111"] = audioStat{saved: 2, total: 3}

	tester := ui.NewTester(app.view, 1120, 760)
	if !tester.HasText("2/3") {
		t.Fatalf("the card has no offline saving chip: %q", tester.Texts())
	}
	if err := tester.Click("朝のリスト"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("2/3") {
		t.Fatalf("the page has no offline saving chip: %q", tester.Texts())
	}
}

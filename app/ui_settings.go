package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	vocabox "github.com/hu-ja-ja/VocaBox_Desktop"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// appCopyright is the line the about card carries.
const appCopyright = "Copyright (C) 2026 hu-ja-ja"

// appRepo is where the app's source lives, which the about card links.
const appRepo = "https://github.com/hu-ja-ja/VocaBox_Desktop"

// settingsView shows the app's settings: the preferences, what the app is,
// and the licences of the works it bundles.
func (app *nativeApp) settingsView(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Padding(24, 24, 16).Gap(18).Children(func() {
		ui.Column(c).Gap(5).Children(func() {
			ui.Text(c, locales.NavSettings()).FontSize(27).Bold()
			ui.Text(c, locales.SettingsSubtitle()).FontSize(13).TextColor(app.theme.TextMuted)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(18).Children(func() {
				app.settingsCard(c)
				app.aboutCard(c)
				app.licensesCard(c)
			})
		})
	})
}

// settingsCard is the preferences the app keeps across runs: the quality the
// audio saved for offline playing is fetched at.
func (app *nativeApp) settingsCard(c *ui.Context) {
	options := make([]string, 0, len(audioQualityPresets))
	for _, kbps := range audioQualityPresets {
		options = append(options, audioQualityLabel(kbps))
	}
	ui.Column(c).Gap(4).Padding(16).Radius(12).Background(app.theme.Background).
		Border(1, app.theme.Border).Children(func() {
		app.settingRow(c, locales.SettingsQualityTitle(), locales.SettingsQualityBody(), func() {
			control := ui.Select(c, &app.qualityOption, options).Label(locales.SettingsQualityTitle())
			if control.Changed() {
				app.setAudioQuality(audioQualityOfLabel(app.qualityOption))
				// The saved audio of the enabled mylists stands at the old
				// quality now: the passes fetch it again.
				app.startAudioDownloads()
			}
		})
	})
}

// aboutCard says what the app is: its name and version, the copyright, the
// licence it is published under, and where its source lives.
func (app *nativeApp) aboutCard(c *ui.Context) {
	ui.Column(c).Gap(12).Padding(16).Radius(12).Background(app.theme.Background).
		Border(1, app.theme.Border).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			app.brandMark(c)
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "VocaBox").FontSize(18).Bold()
				if version := mygo.App.Version(); version != "" {
					ui.Text(c, "v"+version).FontSize(12).TextColor(app.theme.TextMuted)
				}
			})
		})
		ui.Text(c, appCopyright).FontSize(12).TextColor(app.theme.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.SettingsLicense()).FontSize(13).Grow(1)
			button := ui.Button(c, locales.ActionView()).Label(locales.ActionViewOf(locales.SettingsLicense()))
			if button.Clicked() {
				app.openLicense(locales.SettingsLicense(), vocabox.AppLicense)
			}
		})
		ui.Divider(c)
		repo := ui.Button(c, "").Label(locales.SettingsRepository()).AlignSelf(ui.Start).Children(func() {
			ui.Icon(c, iconGitHub).FontSize(15)
			ui.Text(c, locales.SettingsRepository()).FontSize(13)
		})
		if repo.Clicked() {
			c.OpenURL(appRepo)
		}
	})
}

// licensesCard lists the works the app and its playback helper bundle: each
// row names the work and the terms it is under, and opens onto the copyright,
// where it comes from, and the text of those terms.
func (app *nativeApp) licensesCard(c *ui.Context) {
	ui.Column(c).Gap(4).Padding(16).Radius(12).Background(app.theme.Background).
		Border(1, app.theme.Border).Children(func() {
		ui.Text(c, locales.SettingsThirdPartyLicense()).FontSize(14)
		ui.Text(c, locales.SettingsThirdPartyLicenseBody()).FontSize(12).TextColor(app.theme.TextMuted)
		ui.Accordion(c, func() {
			for _, e := range vocabox.Entries {
				key := e.Name + " " + e.Version
				open := app.licenseOpen[key]
				item := ui.AccordionItem(c, licenseTitle(e), &open, func() { app.licenseTerms(c, e) })
				if item.Changed() {
					app.licenseOpen[key] = open
				}
			}
		})
	})
}

// licenseTitle is how one bundled work reads in the list: its name, its
// version, and the terms its text is under.
func licenseTitle(e vocabox.Entry) string {
	title := licenseName(e)
	if e.Version != "" {
		title += " " + e.Version
	}
	if e.License != "" {
		title += "  ·  " + e.License
	}
	return title
}

// licenseName is what the list calls one bundled work: a Go module reads as
// the package it ends in, "mygo" for "github.com/egoist/mygo", which its open
// row spells out in full. A crate is called by its name already.
func licenseName(e vocabox.Entry) string {
	if e.Group != vocabox.GroupGoModules {
		return e.Name
	}
	parts := strings.Split(e.Name, "/")
	// A path ending in a major version, as ".../cobra/v2" does, is named by
	// the element before it.
	if len(parts) > 1 && isMajorVersion(parts[len(parts)-1]) {
		return parts[len(parts)-2]
	}
	return parts[len(parts)-1]
}

// isMajorVersion reports whether a path element names a major version, "v2"
// on.
func isMajorVersion(element string) bool {
	if len(element) < 2 || element[0] != 'v' {
		return false
	}
	_, err := strconv.Atoi(element[1:])
	return err == nil
}

// licenseTerms shows one bundled work open: the module its name shortens, who
// holds its copyright, where it comes from, and the text of the terms.
func (app *nativeApp) licenseTerms(c *ui.Context, e vocabox.Entry) {
	ui.Column(c).Gap(6).Padding(10).Children(func() {
		if e.Group == vocabox.GroupGoModules {
			ui.Text(c, e.Name).FontSize(12).TextColor(app.theme.TextMuted)
		}
		if e.Copyright != "" {
			ui.Text(c, e.Copyright).FontSize(12).TextColor(app.theme.TextMuted)
		}
		if e.URL != "" {
			ui.Link(c, e.URL, e.URL).FontSize(12)
		}
		// The terms run long: they scroll within the row rather than making
		// the page the length of every licence it carries.
		ui.Scroll(c).MaxHeight(260).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				ui.Text(c, e.Text()).FontSize(11).TextColor(app.theme.TextMuted)
			})
		})
	})
}

// openLicense shows a licence's full text in the dialog the about card
// opens.
func (app *nativeApp) openLicense(title, body string) {
	app.licenseTitle, app.licenseBody = title, body
	app.licenseOpenDialog = true
}

// licenseDialog shows the licence the about card opened, long enough to
// scroll.
func (app *nativeApp) licenseDialog(c *ui.Context) {
	ui.Column(c).Width(640).Gap(14).Children(func() {
		ui.Text(c, app.licenseTitle).FontSize(20).Bold()
		ui.Scroll(c).MaxHeight(420).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				ui.Text(c, app.licenseBody).FontSize(12).TextColor(app.theme.TextMuted)
			})
		})
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.PrimaryButton(c, locales.ActionClose()); button.Clicked() {
				app.licenseOpenDialog = false
			}
		})
	})
}

// settingRow is one line of the settings: what the preference does, and the
// control that changes it.
func (app *nativeApp) settingRow(c *ui.Context, title, body string, control func()) {
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).PaddingY(8).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(14)
			ui.Text(c, body).FontSize(12).TextColor(app.theme.TextMuted)
		})
		control()
	})
}

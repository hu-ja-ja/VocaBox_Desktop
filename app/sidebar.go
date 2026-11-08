package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
)

// railWidth is the width of the sidebar closed, sidebarWidth its width
// open, and sidebarPad the inset of its content.
const (
	railWidth             = 64
	sidebarWidth  float32 = 250
	sidebarPad    float32 = 12
	sidebarGutter float32 = 20
)

// The width below which the sidebar's content is the rail: the open
// content fades out down to it, and only one of the two is built at a
// time.
const sidebarSwitchWidth float32 = 160

// The toggle keeps its place on the surface while the content under it
// fades: its size is fixed so the content can hold the same place.
const (
	sidebarToggleWidth  float32 = 36
	sidebarToggleHeight float32 = 30
	accountRowHeight    float32 = 36
)

// sidebarShift is the curve of the sidebar folding between its full width
// and the rail: the content panel follows the width, and the rows that
// enter or reorder keep the same curve.
var sidebarShift = ui.Animation{Duration: 240 * time.Millisecond, Ease: ui.EaseInOut}

func (app *nativeApp) sidebar(c *ui.Context) {
	// The sidebar is one surface that folds between its full width and
	// the rail's: the width it takes is animated in the layout, so the
	// panel and the sidebar stay together, and the content fades to the
	// rail as the width comes down rather than showing beside it.
	target := float32(railWidth)
	if app.sidebarOpen {
		target = sidebarWidth
	}
	area := ui.Column(c).Key("sidebar").FillHeight().ClipX().
		Background(app.theme.Surface).Border(1, app.theme.Border).Radius(12)
	width := area.AnimateTo("width", target, sidebarShift)
	area.Width(width)
	details, alpha := sidebarContent(width)
	area.Children(func() {
		if details {
			app.sidebarDetails(c, alpha)
		} else {
			app.sidebarRail(c, alpha)
		}
		app.accountLine(c, width)
		app.sidebarToggleOverlay(c)
		app.accountOverlay(c)
	})
}

// accountLine is the line above the account: it runs with the surface
// rather than fading with the content, shrinking as the sidebar closes.
func (app *nativeApp) accountLine(c *ui.Context, width float32) {
	left, length := accountLineBox(width)
	ui.Box(c).Absolute().Left(left).Bottom(sidebarGutter + accountRowHeight + sidebarPad).
		Width(length).Height(1).Background(app.theme.Border)
}

// accountLineBox is where the account line runs at a sidebar width: from
// the details' left inset, inside its right padding, and the full inner
// width once the rail takes over.
func accountLineBox(width float32) (left, length float32) {
	t := (sidebarWidth - width) / (sidebarWidth - railWidth)
	t = max(0, min(1, t))
	left = sidebarPad * (1 - t)
	length = width - 2 - (sidebarPad+sidebarGutter)*(1-t)
	return left, length
}

// sidebarToggleOverlay keeps the toggle itself out of the content that
// fades under it, so the menu stays whole while the sidebar folds.
func (app *nativeApp) sidebarToggleOverlay(c *ui.Context) {
	ui.Row(c).Absolute().Left(sidebarPad).Top(sidebarPad).Width(sidebarToggleWidth).Height(48).
		AlignItems(ui.Center).Children(func() {
		app.sidebarToggle(c, !app.sidebarOpen)
	})
}

// accountOverlay keeps the signed-in account's picture on the surface, at
// the same place in both states, clear of the content fading under it.
func (app *nativeApp) accountOverlay(c *ui.Context) {
	if !app.loggedIn {
		return
	}
	size := app.accountAvatarSize()
	ui.Row(c).Absolute().Left(app.accountAvatarInset()).Bottom(sidebarGutter).Width(size).Height(accountRowHeight).
		AlignItems(ui.Center).Children(func() {
		if app.sidebarOpen {
			app.accountAvatar(c)
			return
		}
		app.railAvatar(c)
	})
}

// sidebarContent says whether the sidebar shows its open content at the
// width, and how opaque it is: the open content fades out as the surface
// folds, the rail fades in once the open content is gone, so the two are
// never shown together.
func sidebarContent(width float32) (details bool, alpha float32) {
	if width > sidebarSwitchWidth {
		alpha = (width - sidebarSwitchWidth) / (sidebarWidth - sidebarSwitchWidth)
		return true, max(0, min(1, alpha))
	}
	alpha = (sidebarSwitchWidth - width) / (sidebarSwitchWidth - railWidth)
	return false, max(0, min(1, alpha))
}

// sidebarDetails is the sidebar open: the full width its surface folds
// out to.
func (app *nativeApp) sidebarDetails(c *ui.Context, alpha float32) {
	ui.Column(c).Key("details").Width(sidebarWidth-2).FillHeight().Padding(sidebarPad, sidebarGutter, sidebarGutter, sidebarPad).Gap(16).
		Opacity(alpha).Children(func() {
		ui.Row(c).FillWidth().Height(48).AlignItems(ui.Center).Gap(12).Children(func() {
			// The toggle rides the surface; the box holds its place in
			// the content fading under it.
			ui.Box(c).Size(sidebarToggleWidth, sidebarToggleHeight).Shrink(0)
			lockup := ui.ButtonBase(c).Label(locales.NavSettings()).Tooltip(locales.NavSettings()).
				Grow(1).Justify(ui.Center).Gap(12)
			if lockup.Hovered() {
				lockup.Background(app.theme.SurfaceHover).Radius(10)
			}
			lockup.Children(func() {
				app.brandMark(c)
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, "VocaBox").FontSize(25).Bold()
					ui.Text(c, "NICONICO MUSIC").FontSize(10).FontWeight(700).LetterSpacing(1.2).TextColor(app.theme.TextMuted)
				})
			})
			if lockup.Clicked() {
				app.navigate(settingsPath)
			}
		})

		ui.Divider(c)
		page := app.router.Path()
		// Choosing a destination leaves the queue screen for it, even the
		// mylist already showing, whose click changes no route of its own.
		// The sidebar lists the same destinations as the home: the
		// mylists, and the combined ones under their own heading.
		pinned, own, combined, read := app.mylistGroups()
		entries := append(append(pinned, own...), read...)
		homeClicked, mylistClicked := false, false
		sidebar := ui.Sidebar(c, &page, func() {
			item := ui.SidebarItem(c, homePath, nil, locales.NavHome())
			homeClicked = item.Clicked()
			ui.SidebarSection(c, locales.NavMylists(), nil, func() {
				for _, entry := range entries {
					if app.sidebarMylistItem(c, entry) {
						mylistClicked = true
					}
				}
			})
			if len(combined) > 0 {
				ui.SidebarSection(c, locales.NavCombined(), nil, func() {
					for _, entry := range combined {
						if app.sidebarMylistItem(c, entry) {
							mylistClicked = true
						}
					}
				})
			}
		}).Grow(1)
		if homeClicked || mylistClicked {
			app.queueOpen = false
			app.lyricsOpen = false
		}
		if sidebar.Changed() {
			app.router.Push(page)
		}
		if len(entries) == 0 && len(combined) == 0 {
			ui.Text(c, locales.SidebarEmpty()).FontSize(12).TextColor(app.theme.TextMuted)
		}
		app.accountRow(c)
	})
}

// sidebarMylistItem is one mylist of the sidebar: the way to it, its saving
// mark and pin badge as the home's card carries them, and its right-click
// menu. It reports a click.
func (app *nativeApp) sidebarMylistItem(c *ui.Context, entry mylistEntry) bool {
	item := ui.SidebarItem(c, mylistPath(entry.ID), nil, entry.Name)
	if entry.Download {
		// The sidebar is narrow: the chip's mark alone, without its count.
		item.Children(func() { app.audioBadge(c, app.audioMylistState(entry.ID), 20, 11) })
	}
	if entry.Pinned {
		item.Children(func() { app.pinBadge(c) })
	}
	if item.RightClicked() {
		app.openMylistMenu(item, entry)
	}
	app.mylistMenu(c, item, entry)
	if item.Clicked() {
		app.menuOpen = false
		app.loadingID = ""
		return true
	}
	return false
}

// accountRow shows the sign-in state at the foot of the sidebar: the way
// in while signed out, and the way out while signed in, with the account's
// picture and name once they are fetched.
func (app *nativeApp) accountRow(c *ui.Context) {
	if app.loggedIn {
		ui.Row(c).FillWidth().Height(accountRowHeight).Gap(8).AlignItems(ui.Center).Children(func() {
			// The picture rides the surface, centered in the rail; the
			// boxes hold its place in the content fading under it.
			size := app.accountAvatarSize()
			inset := max(0, app.accountAvatarInset()-sidebarPad)
			ui.Box(c).Width(inset).Shrink(0)
			ui.Box(c).Size(size, size).Shrink(0)
			label := locales.SignedIn()
			if app.accountName != "" {
				label = locales.SignedInAs(app.accountName)
			}
			ui.Text(c, label).FontSize(12).TextColor(app.theme.TextMuted).
				SingleLine().Ellipsis("…").Grow(1).Basis(0).MinWidth(0)
			if button := ui.Button(c, locales.ActionLogout()).Padding(6, 9); button.Clicked() {
				app.confirmLogout = true
			}
		})
		return
	}
	label := locales.LoginWithNiconico()
	if app.loginWindow != nil {
		label = locales.LoginOpening()
	}
	if button := ui.Button(c, label).FillWidth().Height(accountRowHeight).Disabled(app.loginWindow != nil); button.Clicked() {
		app.openLogin()
	}
}

// logoutDialog asks before the session is forgotten, so one click on the
// sidebar's way out does not sign the user out.
func (app *nativeApp) logoutDialog(c *ui.Context) {
	ui.Column(c).Width(320).Gap(14).Children(func() {
		ui.Text(c, locales.LogoutTitle()).FontSize(20).Bold().Role(ui.RoleHeading).Level(2)
		ui.Text(c, locales.LogoutBody()).FontSize(12).TextColor(app.theme.TextMuted)
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if button := ui.Button(c, locales.ActionCancel()).AutoFocus(); button.Clicked() {
				app.confirmLogout = false
			}
			if button := ui.PrimaryButton(c, locales.ActionLogoutConfirm()); button.Clicked() {
				app.confirmLogout = false
				app.logout()
			}
		})
	})
}

// accountAvatarSize is the side of the account's picture, as Avatar draws
// it.
func (app *nativeApp) accountAvatarSize() float32 { return app.theme.Rem(2.25) }

// accountAvatarInset is the picture's left in the closed surface: the
// rail's width less its borders, less the picture, halved.
func (app *nativeApp) accountAvatarInset() float32 {
	return (railWidth - 2 - app.accountAvatarSize()) / 2
}

// accountAvatar shows the signed-in account's picture: an empty circle of
// the same size until it arrives, so the row does not shift when it does.
func (app *nativeApp) accountAvatar(c *ui.Context) {
	if app.accountIcon != nil {
		ui.Avatar(c, app.accountName, app.accountIcon)
		return
	}
	size := app.accountAvatarSize()
	ui.Box(c).Size(size, size).Radius(size / 2).Shrink(0).Background(app.theme.SurfaceHover)
}

// sidebarRail is the sidebar closed: a narrow strip that still navigates —
// the way to the home and to the pinned mylists, and the account — rather
// than only reopening the sidebar.
func (app *nativeApp) sidebarRail(c *ui.Context, alpha float32) {
	ui.Column(c).Key("rail").FillWidth().FillHeight().Padding(sidebarPad, 0, sidebarGutter, 0).Gap(12).
		Opacity(alpha).Children(func() {
		// The toggle rides the surface; the row keeps its place in the
		// content fading under it.
		ui.Box(c).Height(48)
		ui.Row(c).FillWidth().Justify(ui.Start).Children(func() {
			ui.Box(c).Width(sidebarPad).Shrink(0)
			mark := ui.ButtonBase(c).Label(locales.NavSettings()).Tooltip(locales.NavSettings()).Size(36, 36)
			if mark.Hovered() {
				mark.Background(app.theme.SurfaceHover).Radius(10)
			}
			mark.Children(func() { app.brandMark(c) })
			if mark.Clicked() {
				app.navigate(settingsPath)
			}
		})
		ui.Divider(c)
		// The same destinations as the open sidebar's list, the pinned
		// ones: each mylist shows as the picture of its first track.
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().PaddingX(sidebarPad).Gap(6).Children(func() {
				app.railHome(c)
				for _, entry := range app.mylistEntries() {
					if entry.Pinned {
						app.railMylist(c, entry)
					}
				}
			})
		})
		app.railAccount(c)
	})
}

// railHome is the rail's way back to the home, lit while the home shows.
func (app *nativeApp) railHome(c *ui.Context) {
	item := ui.Button(c, "").Label(locales.NavHome()).Tooltip(locales.NavHome()).
		Size(36, 36).Padding(0).BorderWidth(0).Radius(10)
	if app.router.Path() == homePath {
		item.Current(ui.CurrentPage).Background(app.theme.SurfacePressed)
	}
	item.Children(func() { ui.Icon(c, iconHome).FontSize(20) })
	if item.Clicked() {
		app.navigate(homePath)
	}
}

// railMylist shows one pinned mylist as its first track's picture, the way
// to it and its menu; the tooltip names it, and the label marks it as the
// pinned shortcut, apart from the same mylist's name on the home.
func (app *nativeApp) railMylist(c *ui.Context, entry mylistEntry) {
	item := ui.Button(c, "").Label(locales.PinnedName(entry.Name)).Tooltip(entry.Name).
		Size(36, 36).Padding(0).BorderWidth(0).Radius(10)
	if app.router.Path() == mylistPath(entry.ID) {
		item.Current(ui.CurrentPage).Background(app.theme.SurfacePressed)
	}
	item.Children(func() { app.railTile(c, entry.Items) })
	if item.RightClicked() {
		app.openMylistMenu(item, entry)
	}
	app.mylistMenu(c, item, entry)
	if item.Clicked() {
		app.navigate(mylistPath(entry.ID))
	}
}

// railTile shows a mylist's first track's picture, and a list mark until
// the picture arrives, or where the mylist has no track to show.
func (app *nativeApp) railTile(c *ui.Context, items []MylistItem) {
	if len(items) > 0 {
		item := items[0]
		bitmap := app.thumbAt(item.WatchID)
		if bitmap == nil && item.Available {
			app.requestThumbnail(item)
		}
		if bitmap != nil {
			ui.Image(c, bitmap).Size(32, 32).Fit(ui.Cover).Radius(8)
			return
		}
	}
	ui.Box(c).Size(32, 32).Radius(8).Center().Background(app.theme.SurfaceHover).Children(func() {
		ui.Icon(c, iconQueue).FontSize(15).TextColor(app.theme.TextMuted)
	})
}

// railAccount is the rail's foot: the avatar opening the account menu
// while signed in, the way in while not.
func (app *nativeApp) railAccount(c *ui.Context) {
	ui.Row(c).FillWidth().Height(accountRowHeight).Justify(ui.Start).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(sidebarPad).Shrink(0)
		if app.loggedIn {
			// The picture rides the surface; the box holds its place in
			// the content fading under it.
			ui.Box(c).Size(app.accountAvatarSize(), app.accountAvatarSize()).Shrink(0)
			return
		}
		label := locales.LoginWithNiconico()
		button := ui.Button(c, "").Label(label).Tooltip(label).
			Size(36, 36).Padding(0).BorderWidth(0).Radius(10).Disabled(app.loginWindow != nil)
		button.Children(func() { ui.Icon(c, iconLogIn).FontSize(18) })
		if button.Clicked() {
			app.openLogin()
		}
	})
}

// railAvatar is the signed-in account's picture on the rail, opening the
// account menu.
func (app *nativeApp) railAvatar(c *ui.Context) {
	tip := app.accountName
	if tip == "" {
		tip = locales.Account()
	}
	avatar := app.accountAvatarSize()
	button := ui.Button(c, "").Label(locales.Account()).Tooltip(tip).
		HasPopup(ui.PopupMenu).Expanded(app.railAccountOpen).
		Size(avatar, avatar).Padding(0).BorderWidth(0).Radius(10)
	button.Children(func() { app.accountAvatar(c) })
	if button.Clicked() {
		app.railAccountOpen = !app.railAccountOpen
	}
	app.railAccountMenu(c, button)
}

// railAccountMenu is the small menu the rail's avatar opens: who is signed
// in, and the way out that asks before the session is forgotten.
func (app *nativeApp) railAccountMenu(c *ui.Context, anchor ui.Element) {
	ui.PopoverBase(c, anchor, &app.railAccountOpen, func(panel ui.Element) {
		panel.Padding(4).Radius(app.theme.Radius+2).Background(app.theme.Background).
			Border(1, app.theme.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).
			Role(ui.RoleMenu).Label(locales.AccountMenu()).Transition(menuIn)
		name := app.accountName
		if name == "" {
			name = locales.SignedIn()
		}
		ui.Text(c, name).FontSize(12).TextColor(app.theme.TextMuted).
			Padding(6, 8).MaxWidth(200).SingleLine().Ellipsis("…")
		item := app.menuItem(c, iconLogOut, locales.ActionLogout())
		if item.Clicked() {
			app.railAccountOpen = false
			app.confirmLogout = true
		}
	})
}

// brandMark shows the VocaBox mark, the accent tile in the sidebar header
// and the collapsed rail.
func (app *nativeApp) brandMark(c *ui.Context) {
	ui.Box(c).Size(36, 36).Center().Background(app.theme.Accent).Radius(10).Children(func() {
		ui.Text(c, "V").FontSize(18).Bold().TextColor(app.theme.AccentText)
	})
}

// sidebarToggle shows an icon button opening or closing the sidebar; the
// label follows the current state for assistive technology.
func (app *nativeApp) sidebarToggle(c *ui.Context, opening bool) {
	label := locales.SidebarClose()
	if opening {
		label = locales.SidebarOpen()
	}
	button := ui.Button(c, "").Label(label).Tooltip(label).Expanded(app.sidebarOpen).
		BorderWidth(0).Padding(6, 9).Size(sidebarToggleWidth, sidebarToggleHeight).Children(func() {
		ui.Icon(c, iconMenu).FontSize(18)
	})
	if button.Clicked() {
		app.sidebarOpen = opening
	}
}

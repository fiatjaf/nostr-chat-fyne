package main

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/nbd-wtf/go-nostr"
	"github.com/puzpuzpuz/xsync"
	"golang.org/x/net/context"
)

const (
	APP_TITLE = "Nostr Chat"
	APPID     = "com.galaxoidlabs.nostrchat"
	RELAYSKEY = "relays"
)

var baseSize = fyne.Size{Width: 900, Height: 640}

var (
	relays            = xsync.NewMapOf[*ChatRelay]()
	relayMenuData     = make([]LeftMenuItem, 0)
	selectRelayURL    = ""
	selectedGroupName = "/"
)

var (
	a fyne.App
	w fyne.Window
	k Keystore
)

var emptyRelayListOverlay *fyne.Container

func main() {
	a = app.NewWithID(APPID)
	w = a.NewWindow(APP_TITLE)
	w.Resize(baseSize)

	// Keystore might be using the native keyring or falling back to just a file with a key
	k = startKeystore()

	// a.Preferences().RemoveValue(RELAYSKEY)

	// Setup the right side of the window
	var chatMessagesListWidget *widget.List
	chatMessagesListWidget = widget.NewList(
		func() int {
			if relay, ok := relays.Load(selectRelayURL); ok {
				if room, ok := relay.Groups.Load(selectedGroupName); ok {
					return len(room.ChatMessages)
				}
			}
			return 0
		},
		func() fyne.CanvasObject {
			pubKey := canvas.NewText("template", color.RGBA{139, 190, 178, 255})
			pubKey.TextStyle.Bold = true
			pubKey.Alignment = fyne.TextAlignLeading

			message := widget.NewLabel("template")
			message.Alignment = fyne.TextAlignLeading
			message.Wrapping = fyne.TextWrapWord

			vbx := container.NewVBox(container.NewPadded(pubKey))
			border := container.NewBorder(nil, nil, vbx, nil, message)

			return border
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if relay, ok := relays.Load(selectRelayURL); ok {
				if room, ok := relay.Groups.Load(selectedGroupName); ok {
					chatMessage := room.ChatMessages[i]
					pubKey := fmt.Sprintf("[ %s ]", chatMessage.PubKey[len(chatMessage.PubKey)-8:])
					message := chatMessage.Content
					o.(*fyne.Container).Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Text).Text = pubKey
					o.(*fyne.Container).Objects[0].(*widget.Label).SetText(message)
					chatMessagesListWidget.SetItemHeight(i, o.(*fyne.Container).Objects[0].(*widget.Label).MinSize().Height)
				}
			}
		},
	)

	chatInputWidget := widget.NewMultiLineEntry()
	chatInputWidget.Wrapping = fyne.TextWrapWord
	chatInputWidget.SetPlaceHolder("Say something...")
	chatInputWidget.OnSubmitted = func(s string) {
		go func() {
			if s == "" {
				return
			}
			chatInputWidget.SetText("")
			publishChat(s)
		}()
	}

	submitChatButtonWidget := widget.NewButton("Submit", func() {
		message := chatInputWidget.Text
		if message == "" {
			return
		}
		go func() {
			chatInputWidget.SetText("")
			publishChat(message)
		}()
	})

	bottomBorderContainer := container.NewBorder(nil, nil, nil, submitChatButtonWidget, chatInputWidget)

	// Setup the left side of the window
	var relaysListWidget *widget.List
	relaysListWidget = widget.NewList(
		func() int {
			l := len(relayMenuData)
			if l > 0 {
				hideEmptyRelayListOverlay()
			} else {
				showEmptyRelayListOverlay()
			}
			return l
		},
		func() fyne.CanvasObject {
			b := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
			})
			return container.NewHBox(widget.NewLabel("template"), layout.NewSpacer(), b)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) { // CHECK out of index...
			if len(relayMenuData) > i {
				if relayMenuData[i].GroupName == "/" {
					o.(*fyne.Container).Objects[0].(*widget.Label).SetText(relayMenuData[i].RelayURL)
					o.(*fyne.Container).Objects[0].(*widget.Label).TextStyle = fyne.TextStyle{
						Bold:   true,
						Italic: true,
					}
					o.(*fyne.Container).Objects[2].(*widget.Button).OnTapped = func() {
						entry := widget.NewEntry()
						entry.SetPlaceHolder("ex: /pizza")
						dialog.ShowForm("Add Group                                             ", "Add", "Cancel", []*widget.FormItem{ // Empty space Hack to make dialog bigger
							widget.NewFormItem("Group Name", entry),
						}, func(b bool) {
							group := entry.Text
							if group != "" {
								if !strings.HasPrefix(group, "/") {
									group = "/" + group
								}
								addGroup(relayMenuData[i].RelayURL, group, relaysListWidget, chatMessagesListWidget)
							}
						}, w)
					}
					o.(*fyne.Container).Objects[2].Show()
				} else {
					o.(*fyne.Container).Objects[0].(*widget.Label).SetText("    " + relayMenuData[i].GroupName)
					o.(*fyne.Container).Objects[0].(*widget.Label).TextStyle = fyne.TextStyle{
						Bold:   false,
						Italic: false,
					}
					o.(*fyne.Container).Objects[2].Hide()
				}
			}
		},
	)

	relaysListWidget.OnSelected = func(id widget.ListItemID) {
		selectRelayURL = relayMenuData[id].RelayURL
		selectedGroupName = relayMenuData[id].GroupName
		chatMessagesListWidget.Refresh()
		chatMessagesListWidget.ScrollToBottom() // TODO: Probalby need to guard this. For instance if user has scrolled up, it shouldnt jump to bottom on its own
	}

	relaysBottomToolbarWidget := widget.NewToolbar(
		widget.NewToolbarAction(theme.AccountIcon(), func() {
			entry := widget.NewEntry()
			entry.SetPlaceHolder("nsec1...")
			dialog.ShowForm("Import a Nostr Private Key                                             ", "Import", "Cancel", []*widget.FormItem{ // Empty space Hack to make dialog bigger
				widget.NewFormItem("Private Key", entry),
			}, func(b bool) {
				if entry.Text != "" && b {
					err := saveKey(entry.Text) // TODO: Handle Error
					if err != nil {
						fmt.Println("Err saving key: ", err)
					}
				}
			}, w)
		}),
		widget.NewToolbarSpacer(),
		widget.NewToolbarAction(theme.StorageIcon(), func() {
			addRelayDialog(relaysListWidget, chatMessagesListWidget)
		}),
		widget.NewToolbarAction(theme.DeleteIcon(), func() {
			dialog.NewConfirm("Reset local data?", "This will remove all relays and your private key.", func(b bool) {
				if b {
					relays = nil
					relays.Range(func(_ string, chatRelay *ChatRelay) bool {
						chatRelay.Relay.Close()
						return true
					})

					relays = nil
					relayMenuData = nil
					a.Preferences().RemoveValue(RELAYSKEY)
					relaysListWidget.Refresh()
					chatMessagesListWidget.Refresh()

					k.Erase()
				}
			}, w).Show()
		}),
	)

	emptyRelayListOverlay = container.NewCenter(widget.NewButtonWithIcon("Add Relay", theme.StorageIcon(), func() {
		addRelayDialog(relaysListWidget, chatMessagesListWidget)
	}))

	leftBorderContainer := container.NewBorder(nil, container.NewPadded(relaysBottomToolbarWidget), nil, nil, container.NewMax(container.NewPadded(relaysListWidget), emptyRelayListOverlay))
	rightBorderContainer := container.NewBorder(nil, container.NewPadded(bottomBorderContainer), nil, nil, container.NewPadded(chatMessagesListWidget))

	splitContainer := container.NewHSplit(leftBorderContainer, rightBorderContainer)
	splitContainer.Offset = 0.35

	w.SetContent(splitContainer)

	go func() {
		relays := getRelays()
		for _, relay := range relays {
			if relay.URL == "" {
				// TODO: Better relay validation
				continue
			}
			addRelay(relay.URL, relaysListWidget, chatMessagesListWidget)
			for _, group := range relay.Groups {
				addGroup(relay.URL, group, relaysListWidget, chatMessagesListWidget)
			}
		}
	}()

	w.ShowAndRun()
}

func hideEmptyRelayListOverlay() {
	emptyRelayListOverlay.Hide()
}

func showEmptyRelayListOverlay() {
	emptyRelayListOverlay.Show()
}

func addRelayDialog(relaysListWidget *widget.List, chatMessagesListWidget *widget.List) {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("somerelay.com")
	dialog.ShowForm("Add Relay                                             ", "Add", "Cancel", []*widget.FormItem{ // Empty space Hack to make dialog bigger
		widget.NewFormItem("URL", entry),
	}, func(b bool) {
		if entry.Text != "" && b {
			relayURL := entry.Text
			addRelay(relayURL, relaysListWidget, chatMessagesListWidget)
			addGroup(relayURL, "/", relaysListWidget, chatMessagesListWidget)
		}
	}, w)
}

func addGroup(relayURL string, groupId string, relaysListWidget *widget.List, chatMessagesListWidget *widget.List) {
	chatRelay, ok := relays.Load(relayURL)
	if !ok {
		// TODO: Better handling
		fmt.Println("no relay to add group to:", relayURL)
		return
	}

	if g, ok := chatRelay.Groups.Load(groupId); ok {
		fmt.Println("group already there:", g)
		return
	}

	group := &ChatGroup{
		ID:           groupId,
		ChatMessages: make([]*nostr.Event, 0),
	}
	chatRelay.Groups.Store(groupId, group)

	ctx := context.Background()
	sub := chatRelay.Relay.PrepareSubscription(ctx)
	sub.SetLabel("chat" + groupId)
	sub.Filters = []nostr.Filter{{
		Kinds: []int{9},
		Tags: nostr.TagMap{
			"g": {groupId},
		},
	}}

	chatRelay.Subscriptions.Store(groupId, sub)
	saveRelays()
	updateLeftMenuList(relaysListWidget)

	for idx, menuItem := range relayMenuData {
		if menuItem.GroupName == groupId {
			relaysListWidget.Select(idx)
			break
		}
	}

	if err := sub.Fire(); err != nil {
		// TODO: better handling
		panic(err)
	}

	go func() {
		for ev := range sub.Events {
			if ev.Kind == 9 {
				group.ChatMessages = insertEventIntoAscendingList(group.ChatMessages, ev)
				chatMessagesListWidget.Refresh()
				chatMessagesListWidget.ScrollToBottom()
				updateLeftMenuList(relaysListWidget)
			}
		}
	}()
}

func addRelay(relayURL string, relaysListWidget *widget.List, chatMessagesListWidget *widget.List) {
	if !strings.HasPrefix(relayURL, "wss://") && !strings.HasPrefix(relayURL, "ws://") {
		relayURL = "wss://" + relayURL
	}

	if _, ok := relays.Load(relayURL); ok {
		return
	} else {
		ctx := context.Background()
		relay, err := nostr.RelayConnect(ctx, relayURL)
		if err != nil {
			fmt.Println("Err connecting to: ", relayURL)
			return
		}

		chatRelay := &ChatRelay{
			Relay:         *relay,
			Subscriptions: xsync.NewMapOf[*nostr.Subscription](),
			Groups:        xsync.NewMapOf[*ChatGroup](),
		}

		relays.Store(relayURL, chatRelay)
	}
}

func updateLeftMenuList(relaysListWidget *widget.List) {
	relayMenuData = make([]LeftMenuItem, 0)

	relays.Range(func(_ string, chatRelay *ChatRelay) bool {
		flmi := LeftMenuItem{
			RelayURL:  chatRelay.Relay.URL,
			GroupName: "/",
		}
		relayMenuData = append(relayMenuData, flmi)

		chatRelay.Groups.Range(func(_ string, group *ChatGroup) bool {
			if group.ID != "/" {
				lmi := LeftMenuItem{
					RelayURL:  chatRelay.Relay.URL,
					GroupName: group.ID,
				}
				relayMenuData = append(relayMenuData, lmi)
			}

			return true
		})

		return true
	})

	relaysListWidget.Refresh()
}

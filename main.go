package main

import (
	"embed"
	"log"
	"os/exec"
	"runtime"

	"cmdcmd/internal/config"
	"cmdcmd/internal/hotkey"
	"cmdcmd/internal/session"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/tray.png
var trayIcon []byte

func init() {
	application.RegisterEvent[session.Event]("claude")
	application.RegisterEvent[application.Void]("window:shown")
	application.RegisterEvent[application.Void]("session:new")
	application.RegisterEvent[string]("notice")
	application.RegisterEvent[application.Void]("agents:changed")
}

func main() {
	launcher := NewLauncher()

	app := application.New(application.Options{
		Name:        "cmdcmd",
		Description: "Spotlight-style launcher for Claude Code sessions",
		Services: []application.Service{
			application.NewService(launcher),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// No Dock icon, no ⌘-Tab entry, no app menu; cmdcmd lives in the
			// menu bar. LSUIElement in Info.plist does the same before launch.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "com.mebn.cmdcmd",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { launcher.Show() },
		},
		OnShutdown: launcher.shutdown,
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "main",
		Title:           "cmdcmd",
		Width:           windowWidth,
		Height:          windowHeight,
		Frameless:       true,
		DisableResize:   true,
		Hidden:          true,
		AlwaysOnTop:     true,
		HideOnFocusLost: true,
		// Esc is handled in JS: it closes the slash menu first, then hides.
		BackgroundType: application.BackgroundTypeTransparent,
		URL:            "/",
		Mac: application.MacWindow{
			// A non-activating panel takes keyboard input without stealing
			// focus from the app you were in, the way Spotlight does.
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true},
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary |
				application.MacWindowCollectionBehaviorIgnoresCycle,
			WindowLevel: application.MacWindowLevelFloating,
			// The window is an invisible stage; the frontend draws the cards
			// and their shadows. A transparent webview needs
			// -tags private_mac_apis, which the Taskfile sets.
			Backdrop:      application.MacBackdropTransparent,
			DisableShadow: true,
			TitleBar: application.MacTitleBar{
				AppearsTransparent: true,
				Hide:               true,
			},
		},
		Windows: application.WindowsWindow{
			BackdropType:    application.None,
			HiddenOnTaskbar: true,
		},
	})

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIcon)
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("cmdcmd (" + hotkey.Description + ")")
	tray.OnClick(launcher.Toggle)
	tray.SetMenu(trayMenu(app, launcher))

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		launcher.attach(app, window)
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func trayMenu(app *application.App, launcher *Launcher) *application.Menu {
	menu := app.NewMenu()
	menu.Add("Open cmdcmd  " + hotkey.Description).OnClick(func(*application.Context) { launcher.Show() })
	menu.Add("New session").OnClick(func(*application.Context) {
		launcher.NewSession()
		launcher.Show()
	})
	menu.AddSeparator()
	menu.Add("Edit config…").OnClick(func(*application.Context) {
		if path, err := config.Path(); err == nil {
			openFile(path)
		}
	})
	menu.AddSeparator()
	menu.Add("Quit cmdcmd").OnClick(func(*application.Context) { app.Quit() })
	return menu
}

func openFile(path string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", "-t", path).Start()
	case "windows":
		exec.Command("notepad", path).Start()
	default:
		exec.Command("xdg-open", path).Start()
	}
}

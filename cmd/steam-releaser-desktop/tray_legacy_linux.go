//go:build linux && !bindings

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <stdlib.h>

extern void steamReleaserTrayAction(int action);

// GTK 3's deprecated status-icon API is required for native XEmbed trays.
G_GNUC_BEGIN_IGNORE_DEPRECATIONS

static GtkStatusIcon *steam_tray_icon;
static GtkWidget *steam_tray_menu;

static void steam_tray_show(GtkWidget *widget, gpointer data) {
    steamReleaserTrayAction(0);
}

static void steam_tray_exit(GtkWidget *widget, gpointer data) {
    steamReleaserTrayAction(1);
}

static void steam_tray_popup(GtkStatusIcon *icon, guint button, guint activate_time, gpointer data) {
    gtk_menu_popup(GTK_MENU(steam_tray_menu), NULL, NULL,
        gtk_status_icon_position_menu, icon, button, activate_time);
}

// GTK's status icon is the native XEmbed implementation used by i3bar. GTK
// owns these objects on the main thread, alongside Wails' existing GTK loop.
static void steam_tray_start(void *png, int size) {
    GdkPixbufLoader *loader = gdk_pixbuf_loader_new();
    gdk_pixbuf_loader_write(loader, png, size, NULL);
    gdk_pixbuf_loader_close(loader, NULL);
    steam_tray_icon = gtk_status_icon_new_from_pixbuf(gdk_pixbuf_loader_get_pixbuf(loader));
    g_object_unref(loader);
    gtk_status_icon_set_title(steam_tray_icon, "Steam Releaser");
    gtk_status_icon_set_tooltip_text(steam_tray_icon, "Steam Releaser — releases keep running when the window is closed");
    steam_tray_menu = gtk_menu_new();
    GtkWidget *show = gtk_menu_item_new_with_label("Show Steam Releaser");
    GtkWidget *exit = gtk_menu_item_new_with_label("Force Exit");
    gtk_menu_shell_append(GTK_MENU_SHELL(steam_tray_menu), show);
    gtk_menu_shell_append(GTK_MENU_SHELL(steam_tray_menu), gtk_separator_menu_item_new());
    gtk_menu_shell_append(GTK_MENU_SHELL(steam_tray_menu), exit);
    g_signal_connect(show, "activate", G_CALLBACK(steam_tray_show), NULL);
    g_signal_connect(exit, "activate", G_CALLBACK(steam_tray_exit), NULL);
    g_signal_connect(steam_tray_icon, "activate", G_CALLBACK(steam_tray_show), NULL);
    g_signal_connect(steam_tray_icon, "popup-menu", G_CALLBACK(steam_tray_popup), NULL);
    gtk_widget_show_all(steam_tray_menu);
    gtk_status_icon_set_visible(steam_tray_icon, TRUE);
}

static int steam_tray_init(void) {
    return gtk_init_check(NULL, NULL);
}

static void steam_tray_stop(void) {
    if (steam_tray_icon != NULL) {
        gtk_status_icon_set_visible(steam_tray_icon, FALSE);
        g_object_unref(steam_tray_icon);
        steam_tray_icon = NULL;
        gtk_widget_destroy(steam_tray_menu);
        steam_tray_menu = NULL;
    }
}
G_GNUC_END_IGNORE_DEPRECATIONS
*/
import "C"

import (
	"log"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
)

var legacyTrayShow, legacyTrayExit func()

func init() {
	legacyTrayFactory = newLegacySystemTray
}

func newLegacySystemTray(showWindow, forceExit func()) (start, stop func(), available bool) {
	if os.Getenv("DISPLAY") == "" {
		return nil, nil, false
	}
	bus, err := dbus.ConnectSessionBus()
	if err == nil {
		var hasHost bool
		call := bus.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher")
		_ = call.Store(&hasHost)
		_ = bus.Close()
		if hasHost {
			return nil, nil, false
		}
	}
	legacyTrayShow, legacyTrayExit = showWindow, forceExit
	var stopOnce sync.Once
	return func() {
			if C.steam_tray_init() == 0 {
				log.Print("Steam Releaser could not initialize the X11 system tray")
				return
			}
			png := C.CBytes(trayPNG)
			defer C.free(png)
			C.steam_tray_start(png, C.int(len(trayPNG)))
		}, func() {
			stopOnce.Do(func() { C.steam_tray_stop() })
		}, true
}

//export steamReleaserTrayAction
func steamReleaserTrayAction(action C.int) {
	// Never block GTK's main thread while restoring the window or waiting for
	// cancelled work; Wails' runtime calls need that thread to remain responsive.
	if action == 0 {
		go legacyTrayShow()
	} else {
		go legacyTrayExit()
	}
}

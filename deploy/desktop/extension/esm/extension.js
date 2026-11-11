// NoCapOS look for the desktop (GNOME Shell 45 and newer): the NoCapOS logo
// at the left of the top bar, opening the Activities overview. The colours
// live in stylesheet.css. Written by NoCapOS's install.sh.
import Gio from 'gi://Gio';
import St from 'gi://St';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import { Extension } from 'resource:///org/gnome/shell/extensions/extension.js';

const LOGO = '/usr/share/nocapos/desktop/logo.png';

export default class NoCapBrand extends Extension {
  enable() {
    this._button = new PanelMenu.Button(0.0, 'NoCapOS', true);
    this._button.add_style_class_name('nocapos-logo-button');
    this._button.add_child(new St.Icon({ gicon: Gio.icon_new_for_string(LOGO), style_class: 'nocapos-logo' }));
    this._button.connect('button-press-event', () => {
      if (Main.sessionMode.hasOverview) Main.overview.toggle();
    });
    Main.panel.addToStatusArea('nocapos-logo', this._button, 0, 'left');
  }

  disable() {
    this._button?.destroy();
    this._button = null;
  }
}

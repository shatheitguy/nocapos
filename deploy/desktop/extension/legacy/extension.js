// NoCapOS look for the desktop (GNOME Shell 43 and 44): the NoCapOS logo at
// the left of the top bar, opening the Activities overview. The colours live
// in stylesheet.css. Written by NoCapOS's install.sh.
/* exported init */
const { Gio, St } = imports.gi;
const Main = imports.ui.main;
const PanelMenu = imports.ui.panelMenu;

const LOGO = '/usr/share/nocapos/desktop/logo.png';

class NoCapBrand {
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
    if (this._button) this._button.destroy();
    this._button = null;
  }
}

function init() {
  return new NoCapBrand();
}

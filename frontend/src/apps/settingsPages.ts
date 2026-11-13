// Settings' page catalogue, kept apart from the Settings app so universal
// search can list the pages without loading the whole app.
//
// Every name and description comes from the dictionaries (set.<id> and
// set.<id>.desc), and each page lists its options as dictionary keys, so
// search matches what is on screen in either voice.
import type { IconName } from '../components/Icon';
import { translate, tr } from '../i18n';

export type Cat = 'network' | 'general' | 'devices' | 'agents' | 'appearance' | 'desktop' | 'windows' | 'notifications' | 'sound' | 'lock' | 'security' | 'users' | 'account';
export type Sub =
  | 'about' | 'update' | 'access' | 'storage' | 'datetime' | 'language' | 'backup' | 'troubleshoot'
  | 'appearance' | 'desktop' | 'windows'
  | 'notifications' | 'sound'
  | 'security' | 'lock'
  | 'account' | 'users' | 'devices' | 'agents'
  | 'wifi' | `iface:${string}`;

export interface CatInfo {
  id: Cat;
  icon: IconName;
  color: string;
  admin?: boolean;
  /** Sub-pages, in groups (each group is one card on the category page). */
  groups: Sub[][];
}

export const CATS: CatInfo[] = [
  { id: 'network', icon: 'wifi', color: '#0a84ff', admin: true, groups: [] },
  { id: 'general', icon: 'settings', color: '#8e8e93',
    groups: [['about', 'update'], ['access'], ['storage'], ['datetime', 'language'], ['backup', 'troubleshoot']] },
  { id: 'devices', icon: 'phone', color: '#30d158', groups: [] },
  { id: 'agents', icon: 'sparkles', color: '#bf5af2', groups: [] },
  { id: 'appearance', icon: 'palette', color: '#5e5ce6', groups: [] },
  { id: 'desktop', icon: 'launcher', color: '#3a3a3c', groups: [] },
  { id: 'windows', icon: 'maximize', color: '#3a3a3c', groups: [] },
  { id: 'notifications', icon: 'bell', color: '#ff3b30', groups: [] },
  { id: 'sound', icon: 'sound', color: '#ff2d55', groups: [] },
  { id: 'lock', icon: 'lock', color: '#48484a', groups: [] },
  { id: 'security', icon: 'shield', color: '#30a0ff', groups: [] },
  { id: 'users', icon: 'users', color: '#007aff', admin: true, groups: [] },
  { id: 'account', icon: 'user', color: '#007aff', groups: [] },
];

/** General's sub-pages (and Wi-Fi under Network). */
export const SUBS: Record<string, { icon: IconName; color: string; admin?: boolean }> = {
  about: { icon: 'info', color: '#8e8e93' },
  update: { icon: 'download', color: '#8e8e93', admin: true },
  access: { icon: 'monitor', color: '#30d158', admin: true },
  storage: { icon: 'disk', color: '#8e8e93' },
  datetime: { icon: 'clock', color: '#0a84ff' },
  language: { icon: 'globe', color: '#0a84ff' },
  backup: { icon: 'save', color: '#8e8e93', admin: true },
  troubleshoot: { icon: 'wrench', color: '#8e8e93', admin: true },
  wifi: { icon: 'wifi', color: '#0a84ff' },
};

/** Everything searchable: deep-link ids (also used by universal search and older links). */
export interface Entry {
  id: string;
  icon: IconName;
  cat: Cat;
  sub?: Sub;
  admin?: boolean;
  /** Extra English search words (synonyms, technical terms). */
  keywords?: string;
  /** The page's options, as dictionary keys of their labels. */
  options?: string[];
  /** Opens this app instead of a Settings page. */
  app?: string;
}

export const ENTRIES: Entry[] = [
  { id: 'network', cat: 'network', admin: true, icon: 'wifi', keywords: 'internet lan connection',
    options: ['sn.networking'] },
  { id: 'wifi', cat: 'network', sub: 'wifi', admin: true, icon: 'wifi', keywords: 'wireless wlan ssid hotspot',
    options: ['wifi.savedTitle', 'wifi.share', 'wifi.forget', 'wifi.autoConnect'] },
  { id: 'shield', cat: 'network', admin: true, app: 'shield', icon: 'shield',
    keywords: 'firewall ufw ports open exposed block allow deny ssh security ad blocker ads trackers dns blocklist privacy query log',
    options: ['sh.tab.firewall', 'sh.exp.title', 'sh.ov.dns', 'sh.lists.title', 'sh.logs.queries'] },
  { id: 'ethernet', cat: 'network', admin: true, icon: 'ethernet', keywords: 'ip address dhcp static dns gateway router ipv4 lan cable tcp',
    options: ['sn.tcpip'] },
  { id: 'general', cat: 'general', icon: 'settings' },
  { id: 'about', cat: 'general', sub: 'about', icon: 'info', keywords: 'version device hardware docker gpu npu model processor memory serial ip hostname',
    options: ['sp.renameDevice', 'sp.copyDetails'] },
  { id: 'update', cat: 'general', sub: 'update', admin: true, icon: 'download', keywords: 'upgrade version release new packages patches',
    options: ['gu.updateAll', 'gu.check'] },
  { id: 'access', cat: 'general', sub: 'access', admin: true, icon: 'monitor',
    keywords: 'monitor display native local screen hdmi remote access lan other devices phone local-only lock',
    options: ['nd.mode', 'nd.autologin', 'nd.repair', 'nd.diag', 'su.remoteRow'] },
  { id: 'storage', cat: 'general', sub: 'storage', icon: 'disk', keywords: 'disk drive space capacity usage',
    options: ['sp.disks'] },
  { id: 'storageApp', cat: 'general', admin: true, app: 'storage', icon: 'hdd',
    keywords: 'zfs raid mirror raidz pool pools snapshot snapshots rollback scrub cache encrypted folder share shares mount mounts smart health boot drive spin down cloud',
    options: ['stx.nav.boot', 'stx.nav.pools', 'stx.nav.disks', 'stx.nav.snapshots', 'stx.nav.mounts'] },
  { id: 'setup', cat: 'general', sub: 'about', admin: true, icon: 'sparkles', keywords: 'setup wizard welcome first start onboarding again',
    options: ['wiz.step.voice', 'wiz.step.look'] },
  { id: 'datetime', cat: 'general', sub: 'datetime', icon: 'clock', keywords: 'clock time zone calendar week',
    options: ['sp.clock24', 'sp.seconds', 'sp.dateInDock', 'sp.tzAuto', 'sp.timeZone', 'sp.weekStart'] },
  { id: 'language', cat: 'general', sub: 'language', icon: 'globe', keywords: 'locale currency number format gen z genz standard wording slang professional voice',
    options: ['set.lang.voice', 'set.lang.genz', 'set.lang.standard', 'set.lang.languageRow', 'set.lang.region'] },
  { id: 'backup', cat: 'general', sub: 'backup', admin: true, icon: 'save', keywords: 'backup export database restore download',
    options: ['sp.bkDownload', 'sp.restore'] },
  { id: 'troubleshoot', cat: 'general', sub: 'troubleshoot', admin: true, icon: 'wrench', keywords: 'logs debug journal problems errors support',
    options: ['sp.logs'] },
  { id: 'power', cat: 'general', admin: true, icon: 'power', keywords: 'reboot shutdown power off',
    options: ['set.hero.restart', 'set.hero.shutdown'] },
  { id: 'devices', cat: 'devices', icon: 'phone', keywords: 'devices linked squad phone android tablet pair pairing link launcher app qr code mobile limit max how many battery online last seen',
    options: ['ld.nocapId', 'ld.link', 'ld.unlink', 'ld.rename', 'ld.defaultLabel', 'link.addr.title'] },
  { id: 'agents', cat: 'agents', icon: 'sparkles', keywords: 'mcp ai agent token computer use automation assistant approval',
    options: ['ag.letIn', 'ag.loopback', 'ag.add', 'ag.revoke', 'ag.stop', 'ag.scopes'] },
  { id: 'appearance', cat: 'appearance', icon: 'palette', keywords: 'theme glass classic cyber deck dark light accent color colour',
    options: ['pz.theme', 'pz.lightDark', 'pz.accent', 'pz.wallpapers', 'pz.dim', 'pz.brightness', 'pz.icons', 'pz.transparency', 'pz.motion', 'pz.square', 'pz.grid', 'pz.resetRow'] },
  { id: 'wallpaper', cat: 'appearance', icon: 'image', keywords: 'background photo picture',
    options: ['pz.addPhoto', 'pz.deletePhoto', 'pz.dim'] },
  { id: 'desktop', cat: 'desktop', icon: 'launcher', keywords: 'dock taskbar widgets icons grid desktop',
    options: ['pz.position', 'pz.iconSize', 'pz.magnify', 'pz.autoHide', 'pz.recents', 'pz.dots', 'pz.showWidgets', 'pz.iconNames', 'pz.snap', 'pz.cleanUp'] },
  { id: 'windows', cat: 'windows', icon: 'maximize', keywords: 'title bar menu bar',
    options: ['pz.winButtons', 'pz.dblClick'] },
  { id: 'notifications', cat: 'notifications', icon: 'bell', keywords: 'do not disturb dnd silence alerts banners toasts notification center focus',
    options: ['sp.notifyAbout', 'su.focus', 'sp.ntUpdate', 'sp.ntAppUpdates', 'sp.ntAppErrors', 'sp.ntStorage', 'sp.ntBackups', 'sp.ntDevices'] },
  { id: 'sound', cat: 'sound', icon: 'sound', keywords: 'volume mute clicks effects',
    options: ['sp.playFx', 'sp.volume', 'sp.sClicks', 'sp.sWindows', 'sp.sAlerts', 'sp.sLock'] },
  { id: 'security', cat: 'security', icon: 'shield', keywords: '2fa two-factor totp otp authenticator security password privacy',
    options: ['sp.tf', 'sp.changePw'] },
  { id: 'lock', cat: 'lock', icon: 'lock', keywords: 'screensaver screen saver idle auto-lock',
    options: ['sp.lockAfter', 'sp.saver', 'sp.startAfter'] },
  { id: 'account', cat: 'account', icon: 'user', keywords: 'profile avatar picture sign out log out logout sessions signed in devices sign-in alerts',
    options: ['su.changePhoto', 'su.signOut', 'ses.yourTitle', 'ses.endOthers'] },
  { id: 'users', cat: 'users', admin: true, icon: 'users', keywords: 'people roles admin linux accounts groups',
    options: ['ug.addPerson', 'ug.menu.password', 'ug.menu.delete', 'ug.menu.disable', 'ug.tab.groups'] },
];

type Translate = (key: string) => string;

/** A Settings page's name in the current voice (keys set.<id> in the dictionaries). */
export const pageName = (t: Translate, id: string) => t(`set.${id}`);

/** The plain one-line description that goes under a page's name. */
export const pageDesc = (t: Translate, id: string) => t(`set.${id}.desc`);

/** All the words an entry answers to: its names, descriptions and options in both voices, and its keywords. */
export function searchText(e: Entry): string {
  const keys = [`set.${e.id}`, `set.${e.id}.desc`, ...(e.options ?? [])];
  const words = keys.flatMap((k) => [translate('genz', k), translate('standard', k)]);
  return `${words.join(' ')} ${e.keywords ?? ''}`.toLowerCase();
}

/** The options of an entry whose label (in either voice) matches q, in the current voice. */
export function matchingOptions(e: Entry, q: string, t: Translate): string[] {
  return (e.options ?? [])
    .filter((k) => translate('genz', k).toLowerCase().includes(q) || translate('standard', k).toLowerCase().includes(q))
    .map((k) => t(k));
}

/** Every Settings page, for universal search: names in the current voice, matched in both. */
export function settingsPages(): { id: string; label: string; icon: IconName; admin?: boolean; keywords: string; app?: string }[] {
  return ENTRIES.map((e) => ({ id: e.id, label: tr(`set.${e.id}`), icon: e.icon, admin: e.admin, app: e.app, keywords: searchText(e) }));
}

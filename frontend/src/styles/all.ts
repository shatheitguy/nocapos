// Every stylesheet NoCap OS uses, in order: the browser entry (main.tsx) and
// NoCap Desktop's native entry (native/main.tsx) share this one list, so the
// two always look the same.
// Theme fonts are bundled (served by NoCapOS itself — the CSP blocks font CDNs).
import '@fontsource/orbitron/latin-600.css';
import '@fontsource/rajdhani/latin-500.css';
import '@fontsource/rajdhani/latin-600.css';
import '../styles.css';
import '../kit.css';
import './files.css';
import './appstore.css';
import './settings.css';
import './devices.css';
import './squad.css';
import './storage.css';
import './containers.css';
import './containers-app.css';
import './virtualdesk.css';
import './remotedesktop.css';
import './notifications.css';
import './users.css';
import './shield.css';
import './phone.css';
import './notes.css';
import '../themes.css';
import '../glass.css';
// After the themes: Photos is always dark glass and keeps its own button styles.
import './photos.css';
import './media.css';
// After every app's styles: the shared dialog frame and its close button win.
import './modal.css';
import './picker.css';
import './mobile.css';

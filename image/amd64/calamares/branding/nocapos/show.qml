/* NoCapOS installer slideshow: what NoCapOS does, shown while it installs.
 * Calamares slideshow API 2 (calamares.slideshow 1.0).
 */
import QtQuick 2.0;
import calamares.slideshow 1.0;

Presentation
{
    id: presentation

    Timer {
        id: advance
        interval: 9000
        repeat: true
        running: presentation.activatedInCalamares
        onTriggered: presentation.goToNextSlide()
    }

    function onActivate() { presentation.currentSlide = 0; }
    function onLeave() { }

    NoCapSlide {
        kicker: qsTr("WELCOME")
        heading: qsTr("NoCapOS is on its way")
        body: qsTr("Your own home server and desktop in one. Sit back: this takes a few minutes, and the computer restarts into NoCapOS when it's done.")
    }

    NoCapSlide {
        kicker: qsTr("NOCAP DESKTOP")
        heading: qsTr("A real desktop on this screen")
        body: qsTr("Sign in with the account you just made. Files, a web browser, Settings, a terminal and your apps sit in the dock, all in NoCapOS's dark look.")
    }

    NoCapSlide {
        kicker: qsTr("FROM ANY DEVICE")
        heading: qsTr("Open it in a browser, anywhere at home")
        body: qsTr("Go to http://nocap.local from your phone or laptop and sign in with the same account. It's an administrator, so everything is yours to set up.")
    }

    NoCapSlide {
        kicker: qsTr("APP STORE")
        heading: qsTr("One click, one app")
        body: qsTr("Media servers, photo libraries, password vaults and more install in one click and keep their data on this machine. Linux desktop apps too.")
    }

    NoCapSlide {
        kicker: qsTr("STORAGE")
        heading: qsTr("Drives, pools and snapshots")
        body: qsTr("Combine drives into mirrored or RAID-Z pools with ZFS, take snapshots on a schedule, roll back in seconds and share folders with every device at home.")
    }

    NoCapSlide {
        kicker: qsTr("VIRTUAL MACHINES")
        heading: qsTr("Other systems, right inside")
        body: qsTr("Run virtual machines with UEFI and TPM, and open their screens in the browser.")
    }

    NoCapSlide {
        kicker: qsTr("SHIELD")
        heading: qsTr("Firewall and ad blocking built in")
        body: qsTr("See what's exposed, turn ports on and off with safe presets, and block ads and trackers for the whole network.")
    }

    NoCapSlide {
        kicker: qsTr("UPDATES")
        heading: qsTr("Debian underneath, always patched")
        body: qsTr("Security updates come straight from Debian and install themselves every day. NoCapOS and everything else update from Settings → Software Update.")
    }
}

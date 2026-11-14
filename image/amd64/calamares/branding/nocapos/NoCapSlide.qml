/* One slide of the NoCapOS installer slideshow (show.qml): logo, a red
 * kicker line, a heading and a short text on near-black.
 */
import QtQuick 2.0;
import calamares.slideshow 1.0;

Slide {
    id: s
    property string kicker: ""
    property string heading: ""
    property string body: ""

    Rectangle {
        anchors.fill: parent
        color: "#050508"
        clip: true

        Rectangle {
            id: glow
            width: parent.width * 0.7
            height: width
            radius: width / 2
            x: parent.width * 0.55
            y: -height * 0.45
            color: "#e5383b"
            opacity: 0.10
        }

        Image {
            id: mark
            source: "logo.png"
            width: 56; height: 56
            fillMode: Image.PreserveAspectFit
            anchors.left: parent.left
            anchors.top: parent.top
            anchors.leftMargin: 40
            anchors.topMargin: 32
        }

        Text {
            id: kickerText
            text: s.kicker
            color: "#e5383b"
            font.pixelSize: 15
            font.bold: true
            font.letterSpacing: 2
            anchors.left: mark.left
            anchors.top: mark.bottom
            anchors.topMargin: 28
        }

        Text {
            id: headingText
            text: s.heading
            color: "#ffffff"
            font.pixelSize: 32
            font.bold: true
            wrapMode: Text.WordWrap
            width: parent.width - 80
            anchors.left: mark.left
            anchors.top: kickerText.bottom
            anchors.topMargin: 10
        }

        Rectangle {
            id: bar
            width: 48; height: 4; radius: 2
            color: "#e5383b"
            anchors.left: mark.left
            anchors.top: headingText.bottom
            anchors.topMargin: 18
        }

        Text {
            text: s.body
            color: "#c9c9d1"
            font.pixelSize: 17
            lineHeight: 1.3
            wrapMode: Text.WordWrap
            width: parent.width - 80
            anchors.left: mark.left
            anchors.top: bar.bottom
            anchors.topMargin: 18
        }
    }
}

package com.nocap.link.ui

import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/**
 * The NoCapOS web desktop's stroke icons (24 px grid, round caps), so the
 * phone app draws exactly the same glyphs as the web OS.
 */
object NcIcons {
    private val paths: Map<String, String> = mapOf(
        "launcher" to "M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z",
        "monitor" to "M3 12h4l3-7 4 14 3-7h4",
        "settings" to "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z",
        "desktop" to "M3 5h18v11H3zM8 20h8M12 16v4",
        "info" to "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v6M12 7.5v.01",
        "power" to "M12 3v9M6.3 6.3a8 8 0 1 0 11.4 0",
        "lock" to "M6 11h12v10H6zM8.5 11V7.5a3.5 3.5 0 0 1 7 0V11",
        "moon" to "M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z",
        "sun" to "M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
        "auto" to "M12 21a9 9 0 1 0 0-18v18z M12 3a9 9 0 0 1 0 18",
        "close" to "M6 6l12 12M18 6 6 18",
        "minimize" to "M6 12h12",
        "maximize" to "M5 5h14v14H5z",
        "restore" to "M8 8h11v11H8zM5 16V5h11",
        "search" to "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4",
        "play" to "M7 5v14l12-7z",
        "stop" to "M6 6h12v12H6z",
        "restart" to "M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4",
        "logs" to "M5 4h14v16H5zM8 8h8M8 12h8M8 16h5",
        "cpu" to "M7 7h10v10H7zM10 10h4v4h-4zM9 3v4M15 3v4M9 17v4M15 17v4M3 9h4M3 15h4M17 9h4M17 15h4",
        "memory" to "M3 8h18v8H3zM7 8v8M11 8v8M15 8v8M6 16v3M18 16v3",
        "disk" to "M4 5h16v14H4zM4 14h16M8 17h.01",
        "gpu" to "M3 7h18v10H3zM7 11a1.5 1.5 0 1 0 3 0 1.5 1.5 0 0 0-3 0M14 11a1.5 1.5 0 1 0 3 0 1.5 1.5 0 0 0-3 0M6 17v3",
        "network" to "M4 8a12 12 0 0 1 16 0M7 11.5a7.5 7.5 0 0 1 10 0M10 15a3 3 0 0 1 4 0M12 18.5v.01",
        "user" to "M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0",
        "check" to "M5 12.5 10 17 19 7",
        "alert" to "M12 9v4M12 17v.01M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z",
        "arrowDown" to "M12 5v14M6 13l6 6 6-6",
        "arrowUp" to "M12 19V5M6 11l6-6 6 6",
        "palette" to "M12 21a9 9 0 1 1 9-9c0 2-1.5 3-3.5 3H16a2 2 0 0 0-1.4 3.4A1.6 1.6 0 0 1 13.5 21zM7.5 11.5h.01M10 7.5h.01M15 7.5h.01",
        "image" to "M4 5h16v14H4zM4 16l5-5 4 4 2-2 5 5M15.5 9.5h.01",
        "pin" to "M9 4h6l-1 6 3 3H7l3-3zM12 13v7",
        "trash" to "M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13",
        "terminal" to "M4 5h16v14H4zM7 9l3 3-3 3M12 15h5",
        "files" to "M3 6h6l2 2h10v11H3z",
        "folder" to "M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2h8.5A1.5 1.5 0 0 1 21 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5z",
        "folderPlus" to "M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2h8.5A1.5 1.5 0 0 1 21 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5zM12 10.5v5M9.5 13h5",
        "file" to "M6 3h8l4 4v14H6zM14 3v4h4",
        "fileText" to "M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h6",
        "fileCode" to "M6 3h8l4 4v14H6zM14 3v4h4M10 12l-2 2 2 2M14 12l2 2-2 2",
        "fileImage" to "M6 3h8l4 4v14H6zM14 3v4h4M8 18l3-3 2 2 1.5-1.5L17 18M10 11.5h.01",
        "fileVideo" to "M6 3h8l4 4v14H6zM14 3v4h4M10.5 11.5v5l4-2.5z",
        "fileAudio" to "M6 3h8l4 4v14H6zM14 3v4h4M11 17.5a1.5 1.5 0 1 1-1.5-1.5H11v-5l3.5-1",
        "fileArchive" to "M6 3h8l4 4v14H6zM14 3v4h4M11 6h2M11 9h2M11 12h2M10.5 15h3v3h-3z",
        "filePdf" to "M6 3h8l4 4v14H6zM14 3v4h4M8.5 17v-4h1.5a1.3 1.3 0 0 1 0 2.6H8.5M13 13v4h1a2 2 0 0 0 0-4z",
        "upload" to "M12 16V4M7 9l5-5 5 5M4 16v4h16v-4",
        "download" to "M12 4v12M7 11l5 5 5-5M4 20h16",
        "grid" to "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
        "list" to "M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01",
        "columns" to "M4 4h16v16H4zM12 4v16",
        "sidebarRight" to "M4 4h16v16H4zM15 4v16",
        "swap" to "M7 7h13l-4-4M17 17H4l4 4",
        "chevronLeft" to "M15 5l-7 7 7 7",
        "chevronRight" to "M9 5l7 7-7 7",
        "chevronUp" to "M5 15l7-7 7 7",
        "chevronDown" to "M5 9l7 7 7-7",
        "clock" to "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2",
        "sound" to "M4 9v6h4l5 4V5L8 9zM16 9a4 4 0 0 1 0 6M18.5 6.5a8 8 0 0 1 0 11",
        "wifi" to "M2.5 9a14 14 0 0 1 19 0M5.5 12.5a9.5 9.5 0 0 1 13 0M8.6 16a5 5 0 0 1 6.8 0M12 19.5h.01",
        "ethernet" to "M5 4h14v10H5zM9 14v3M12 14v3M15 14v3M8 20h8M8 8h2M14 8h2",
        "users" to "M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2.5 20a6.5 6.5 0 0 1 13 0M16 4.5a3.5 3.5 0 0 1 0 6.5M18 14a6 6 0 0 1 3.5 6",
        "logout" to "M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10",
        "key" to "M8 15a4 4 0 1 1 0-8 4 4 0 0 1 0 8zM11.5 11H21M18 11v3M15 11v2",
        "sliders" to "M4 7h9M17 7h3M15 5v4M4 17h3M11 17h9M9 15v4",
        "globe" to "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18",
        "shield" to "M12 3l7 3v5c0 4.5-3 8.3-7 10-4-1.7-7-5.5-7-10V6z",
        "scissors" to "M6 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM8.1 7.9 20 20M8.1 16.1 20 4",
        "copy" to "M9 9h11v11H9zM5 15V4h11",
        "clipboard" to "M8 4h8v3H8zM8 5.5H5V21h14V5.5h-3",
        "edit" to "M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4",
        "eye" to "M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
        "home" to "M4 11 12 4l8 7v9h-5v-6H9v6H4z",
        "drive" to "M3 14h18v6H3zM5 14l2.5-9h9L19 14M7 17h.01",
        "save" to "M5 4h11l3 3v13H5zM8 4v5h7V4M8 20v-6h8v6",
        "external" to "M14 4h6v6M20 4l-9 9M18 14v6H4V6h6",
        "sparkles" to "M12 3l1.8 4.9L19 9.7l-5.2 1.8L12 16l-1.8-4.5L5 9.7l5.2-1.8zM18 14l.9 2.3 2.3.9-2.3.9L18 20l-.9-2.3-2.3-.9 2.3-.9z",
        "send" to "M4 12l16-8-6 16-3-6-7-2z",
        "plus" to "M12 5v14M5 12h14",
        "brain" to "M9 4a3 3 0 0 0-3 3 3 3 0 0 0-1 5 3 3 0 0 0 2 4 2.5 2.5 0 0 0 5 .5V5a2 2 0 0 0-3-1zM15 4a3 3 0 0 1 3 3 3 3 0 0 1 1 5 3 3 0 0 1-2 4 2.5 2.5 0 0 1-5 .5",
        "pencil" to "M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4",
        "menu" to "M4 6h16M4 12h16M4 18h16",
        "eject" to "M12 5l7 8H5zM5 18h14",
        "rewind" to "M3 12a9 9 0 1 0 2.6-6.4L3 8M3 3v5h5M12 7.5V12l3 2",
        "heart" to "M12 20s-7.5-4.6-7.5-10.2A4.3 4.3 0 0 1 12 7a4.3 4.3 0 0 1 7.5 2.8C19.5 15.4 12 20 12 20z",
        "albums" to "M4 9h16v11H4zM6 6h12M8 3h8",
        "more" to "M5 12h.01M12 12h.01M19 12h.01",
        "thermometer" to "M10 13.5V5a2 2 0 0 1 4 0v8.5a4 4 0 1 1-4 0zM12 9v7",
        "bell" to "M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15zM10 20.5a2 2 0 0 0 4 0",
        "wrench" to "M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.8-3.8a6 6 0 0 1-7.9 7.9l-6.9 6.9a2.1 2.1 0 0 1-3-3l6.9-6.9a6 6 0 0 1 7.9-7.9z",
        "sortArrows" to "M7 4v16M3.5 7.5 7 4l3.5 3.5M17 20V4M13.5 16.5 17 20l3.5-3.5",
        "star" to "M12 3.5l2.6 5.3 5.9.9-4.3 4.1 1 5.8-5.2-2.7-5.2 2.7 1-5.8-4.3-4.1 5.9-.9z",
        "share" to "M12 15V3.5M8 7.5l4-4 4 4M5 12v7.5h14V12",
        "archiveBox" to "M3.5 4h17v4h-17zM5 8v12h14V8M10 12h4",
        "moreDots" to "M4.8 12a1.2 1.2 0 1 0 2.4 0a1.2 1.2 0 1 0-2.4 0M10.8 12a1.2 1.2 0 1 0 2.4 0a1.2 1.2 0 1 0-2.4 0M16.8 12a1.2 1.2 0 1 0 2.4 0a1.2 1.2 0 1 0-2.4 0",
        "hdd" to "M5 3h14a1 1 0 0 1 1 1v16a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM12 14a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 10l3.5 7M7.5 18h.01",
        "ssd" to "M5 3h14v18H5zM8.5 7h7M8.5 10.5h7M8.5 14h4M16 17.5h.01",
        "nvme" to "M2.5 8.5h19v7h-19zM6 8.5v7M9 11h3v2H9zM14 11h3v2h-3zM21.5 11h-1.5v2h1.5",
        "usb" to "M7 9h10v12H7zM9 3h6v6H9zM11 5.5h.01M13 5.5h.01M10 16h4",
        "layers" to "M12 3 3 7.5l9 4.5 9-4.5zM3 12l9 4.5 9-4.5M3 16.5 12 21l9-4.5",
        "camera" to "M4 8h3l2-3h6l2 3h3v11H4zM12 16a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
        "database" to "M12 4c4.4 0 8 1.3 8 3s-3.6 3-8 3-8-1.3-8-3 3.6-3 8-3zM4 7v10c0 1.7 3.6 3 8 3s8-1.3 8-3V7M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
        "pause" to "M8.5 5v14M15.5 5v14",
        "phone" to "M7.5 2.5h9A1.5 1.5 0 0 1 18 4v16a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 6 20V4a1.5 1.5 0 0 1 1.5-1.5zM10.5 18.5h3",
        "tablet" to "M5 3h14a1.5 1.5 0 0 1 1.5 1.5v15A1.5 1.5 0 0 1 19 21H5a1.5 1.5 0 0 1-1.5-1.5v-15A1.5 1.5 0 0 1 5 3zM11 18h2",
        "devices" to "M2.5 5h13v9.5h-13zM6 19h6M9 14.5V19M17.5 9h3.5a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1h-3.5a1 1 0 0 1-1-1v-9a1 1 0 0 1 1-1zM19.25 17.5h.01",
        "rotate" to "M20 12a8 8 0 1 1-2.3-5.7M20 4v4h-4",
        "zoomIn" to "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4M8 11h6M11 8v6",
        "zoomOut" to "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4M8 11h6",
        "fullscreen" to "M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5",
        "fullscreenExit" to "M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5",
        "pip" to "M3 5h18v14H3zM12 12h7v5h-7z",
        "subtitles" to "M3 5h18v14H3zM7 12h3M13 12h4M7 15.5h6M15 15.5h2",
        "shuffle" to "M3 7h3.5c2 0 3.2 1 4.3 2.7l2.4 4.6c1.1 1.7 2.3 2.7 4.3 2.7H21M3 17h3.5c1.3 0 2.2-.4 3-1.2M14 8.2c.8-.8 1.7-1.2 3-1.2H21M18 4l3 3-3 3M18 14l3 3-3 3",
        "repeat" to "M4 11V9a3 3 0 0 1 3-3h13M17 3l3 3-3 3M20 13v2a3 3 0 0 1-3 3H4M7 21l-3-3 3-3",
        "skipBack" to "M18 5v14l-10-7zM6 5v14",
        "skipForward" to "M6 5v14l10-7zM18 5v14",
        "volume" to "M4 9v6h4l5 4V5L8 9zM16.5 8.5a5 5 0 0 1 0 7M19 6a8.5 8.5 0 0 1 0 12",
        "mute" to "M4 9v6h4l5 4V5L8 9zM17 9l5 5M22 9l-5 5",
        "music" to "M9 18V5l11-2v13M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0zM20 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0z",
        "mapPin" to "M12 21s-7-6.2-7-11.5a7 7 0 0 1 14 0C19 14.8 12 21 12 21zM12 12a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z",
        "queue" to "M4 6h12M4 11h12M4 16h7M15 15v6l5-3z",
        "appData" to "M4 6.5C4 5.1 7.6 4 12 4s8 1.1 8 2.5S16.4 9 12 9 4 7.9 4 6.5zM4 6.5v5C4 12.9 7.6 14 12 14s8-1.1 8-2.5v-5M4 11.5v6C4 18.9 7.6 20 12 20c1.2 0 2.3-.1 3.3-.3M17 15h5v5h-5z",
    )

    private val cache = HashMap<String, ImageVector>()

    val names: Set<String> get() = paths.keys

    /** The icon called [name] (the same names as the web OS); a dot when unknown. */
    fun get(name: String): ImageVector = synchronized(cache) {
        cache.getOrPut(name) {
            val d = paths[name] ?: "M12 12h.01"
            ImageVector.Builder(name = name, defaultWidth = 24.dp, defaultHeight = 24.dp, viewportWidth = 24f, viewportHeight = 24f)
                .addPath(
                    pathData = PathParser().parsePathString(d).toNodes(),
                    fill = null,
                    stroke = SolidColor(Color.Black),
                    strokeLineWidth = 1.8f,
                    strokeLineCap = StrokeCap.Round,
                    strokeLineJoin = StrokeJoin.Round,
                )
                .build()
        }
    }
}

/** One NoCapOS icon, tinted. */
@Composable
fun NcIcon(name: String, tint: Color, size: Dp = 20.dp, desc: String? = null, modifier: Modifier = Modifier) {
    Icon(NcIcons.get(name), desc, modifier.size(size), tint = tint)
}

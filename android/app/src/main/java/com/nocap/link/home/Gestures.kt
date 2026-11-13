package com.nocap.link.home

import android.view.HapticFeedbackConstants
import android.view.View
import com.nocap.link.data.Act
import com.nocap.link.data.Gesture
import com.nocap.link.data.Opt
import com.nocap.link.data.t
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.system.Phone

/** Names for gestures and actions, in both voices. */
fun gestureName(g: Gesture) = when (g) {
    Gesture.SWIPE_UP -> t("Swipe up", "Swipe up")
    Gesture.SWIPE_DOWN -> t("Swipe down", "Swipe down")
    Gesture.DOUBLE_TAP -> t("Double-tap", "Double-tap")
    Gesture.PINCH_IN -> t("Pinch in", "Pinch in")
    Gesture.PINCH_OUT -> t("Pinch out", "Pinch out")
    Gesture.TWO_UP -> t("Two-finger swipe up", "Two-finger swipe up")
    Gesture.TWO_DOWN -> t("Two-finger swipe down", "Two-finger swipe down")
    Gesture.HOME -> t("Home button (while home)", "Home button on the home screen")
}

fun actName(a: Act) = when (a) {
    Act.NONE -> t("Nothing", "Do nothing")
    Act.DRAWER -> t("The Roster", "App drawer")
    Act.SEARCH -> t("Search", "Search")
    Act.AI -> t("NoCap AI", "NoCap AI")
    Act.CONTROL_CENTER -> t("NoCap control center", "NoCap control center")
    Act.NOTIFICATIONS -> t("Android's shade (notifications)", "System notification shade")
    Act.QUICK_SETTINGS -> t("Quick settings", "Quick settings")
    Act.LOCK -> t("Lock screen", "Lock screen")
    Act.APP -> t("Open an app…", "Open an app…")
    Act.DESKTOP -> t("My NoCap desktop", "NoCap desktop")
    Act.HUB -> t("NoCap Link", "NoCap Link")
    Act.SETTINGS -> t("NoCap OS settings", "Launcher settings")
    Act.WIDGETS -> t("Add widgets", "Add widgets")
    Act.FIRST_PAGE -> t("First page", "Go to the first page")
}

fun actOf(binding: String): Act = Act.entries.firstOrNull { binding == it.name || (it == Act.APP && binding.startsWith("APP:")) } ?: Act.NONE

/** Human text for a binding, e.g. "Open Camera". */
fun bindingName(a: MainActivity, binding: String): String {
    val act = actOf(binding)
    if (act == Act.APP) {
        val key = binding.removePrefix("APP:")
        val label = a.phoneApps.firstOrNull { it.key == key }?.label ?: key.substringBefore('/')
        return t("Open $label", "Open $label")
    }
    return actName(act)
}

/**
 * Runs what a gesture is bound to. [firstPage] scrolls the home pager.
 * Returns false when there's nothing bound.
 */
fun runGesture(a: MainActivity, g: Gesture, firstPage: () -> Unit): Boolean {
    val b = Opt.binding(g)
    return runAction(a, b, firstPage)
}

fun runAction(a: MainActivity, binding: String, firstPage: () -> Unit): Boolean {
    val act = actOf(binding)
    if (act == Act.NONE) return false
    // Gestures run from touch handlers and effects: a screen that won't open must never take the launcher down.
    runCatching { doAction(a, act, binding, firstPage) }
        .onFailure { a.toast = t("Couldn't open that. Try again.", "That couldn't be opened.") }
    return true
}

private fun doAction(a: MainActivity, act: Act, binding: String, firstPage: () -> Unit) {
    when (act) {
        Act.NONE -> {}
        Act.DRAWER -> a.openDrawer()
        Act.SEARCH -> a.layer = Layer.SEARCH
        Act.AI -> a.openAi()
        Act.CONTROL_CENTER -> a.layer = Layer.CONTROL
        Act.NOTIFICATIONS -> if (!Phone.expandNotifications(a)) a.layer = Layer.SEARCH
        Act.QUICK_SETTINGS -> if (!Phone.expandQuickSettings(a)) Phone.expandNotifications(a)
        Act.LOCK -> if (!Phone.lock(a)) a.toast = t("Turn on the lock helper in Settings → Gestures first.", "Enable the lock helper in Settings → Gestures first.")
        Act.APP -> {
            val key = binding.removePrefix("APP:")
            val app = a.phoneApps.firstOrNull { it.key == key }
            if (app != null) a.launch(app) else a.toast = t("That app dipped. Pick another in Settings → Gestures.", "That app isn't installed anymore.")
        }
        Act.DESKTOP -> a.openDesktop()
        Act.HUB -> a.layer = Layer.HUB
        Act.SETTINGS -> a.layer = Layer.SETTINGS
        Act.WIDGETS -> if (Opt.lockLayout.v) a.toast = lockedMsg() else a.layer = Layer.WIDGETS
        Act.FIRST_PAGE -> firstPage()
    }
}

fun lockedMsg() = t("Layout's locked. Unlock it in Settings → Home.", "The home layout is locked. Unlock it in Settings → Home screen.")

/** Haptics, only when switched on in Settings. */
fun buzz(v: View, long: Boolean = false) {
    if (!Opt.haptics.v) return
    v.performHapticFeedback(if (long) HapticFeedbackConstants.LONG_PRESS else HapticFeedbackConstants.KEYBOARD_TAP)
}

package com.nocap.link.data

import android.content.Context

/** NoCap Link settings for staying connected (kept apart from [Opt] so it stays small). */
object LinkOpt {
    /** Keep a live connection to the server in the background (foreground service). */
    val alwaysOn = BoolPref("link_always_on", true)
    /** Show calls on NoCap OS (needs notification access; Decline needs the phone-calls permission). */
    val calls = BoolPref("link_calls", false)

    fun init(ctx: Context) {
        Opt.init(ctx)
        val sp = Opt.sp ?: return
        alwaysOn.load(sp)
        calls.load(sp)
    }
}

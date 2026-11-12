package com.nocap.link

import android.app.Application
import com.nocap.link.data.Prefs
import com.nocap.link.data.Vault
import com.nocap.link.sync.Notices
import com.nocap.link.sync.Sync
import com.nocap.link.system.CrashLog

class NoCapApp : Application() {
    override fun onCreate() {
        super.onCreate()
        // First, so a crash anywhere after this is kept for Settings → About.
        CrashLog.install(this)
        Prefs.init(this)
        Notices.channel(this)
        // The vault is encrypted with a key in the Android Keystore; a broken
        // keystore must not stop the launcher from starting.
        val linked = runCatching { Vault.get(this).link != null }.getOrDefault(false)
        if (linked) HeartbeatWorker.schedule(this)
        Sync.start(this)
        // Opt-in NoCap lock screen and control-center edge strip (both do nothing unless switched on).
        if (com.nocap.link.data.Opt.lockScreen.v) com.nocap.link.lock.LockWatch.start(this)
        runCatching { com.nocap.link.control.EdgeStrip.sync(this) }
    }
}

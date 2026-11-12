package com.nocap.link.control

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import com.nocap.link.data.Prefs
import com.nocap.link.MainActivity
import com.nocap.link.ui.NoCapTheme

/**
 * The NoCap control center over whatever app is open, from the opt-in edge
 * strip (Settings → Gestures → Control center). See-through; leaves no trace
 * in Recents.
 */
class ControlActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        val host = object : ControlHost {
            override val ctx: Context get() = this@ControlActivity
            override fun toast(msg: String) { Toast.makeText(applicationContext, msg, Toast.LENGTH_SHORT).show() }
            override fun close() { finish() }
            override fun openHub() {
                runCatching { startActivity(Intent(this@ControlActivity, MainActivity::class.java).setAction(MainActivity.ACTION_HUB).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
                finish()
            }
            override fun serverLine(): ServerGlance.Line = ServerGlance.read(this@ControlActivity)
        }
        setContent {
            NoCapTheme {
                val shown = remember { MutableTransitionState(false) }
                LaunchedEffect(Unit) { shown.targetState = true }
                AnimatedVisibility(shown, enter = fadeIn() + slideInVertically { -it / 3 }, exit = fadeOut() + slideOutVertically { -it / 3 }) {
                    ControlCenter(host)
                }
            }
        }
    }

    override fun onStop() {
        super.onStop()
        // A see-through panel: once something else is in front, it's done.
        if (!isChangingConfigurations) finish()
    }

    override fun finish() {
        super.finish()
        @Suppress("DEPRECATION")
        overridePendingTransition(0, android.R.anim.fade_out)
    }
}

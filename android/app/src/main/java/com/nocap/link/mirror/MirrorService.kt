package com.nocap.link.mirror

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.ImageReader
import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import android.os.Looper
import android.view.Display
import android.view.Surface
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.IntentCompat
import com.nocap.link.R
import com.nocap.link.data.Link
import com.nocap.link.data.Prefs
import com.nocap.link.data.Vault
import com.nocap.link.data.t
import com.nocap.link.net.Pins
import com.nocap.link.system.MirrorInput
import java.io.ByteArrayOutputStream
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString

/**
 * Mirrors this phone's screen to NoCap OS: MediaProjection → a virtual
 * display → the hardware H.264 encoder (or JPEG pictures for windows that
 * can't decode H.264) → one WebSocket to your own server. A foreground
 * service with an ongoing "Your screen is being mirrored" notification and
 * a Stop button. Only runs after you said yes, here and in Android's prompt.
 */
class MirrorService : Service() {
    private lateinit var thread: HandlerThread
    private lateinit var h: Handler
    private val main = Handler(Looper.getMainLooper())

    private var link: Link? = null
    private var ask: String? = null
    private var projection: MediaProjection? = null
    private var projCb: MediaProjection.Callback? = null
    private var display: VirtualDisplay? = null
    private var codec: MediaCodec? = null
    private var reader: ImageReader? = null
    private var client: OkHttpClient? = null
    private var ws: WebSocket? = null
    private var wsGen = 0
    private var failures = 0
    private var stopping = false

    // Pipeline state (all on [h]).
    private var jpeg = false
    private var w = 0
    private var h0 = 0
    private var rot = 0
    private var screen = 0 to 0
    private var config: ByteArray? = null
    private var needKey = false
    private var lastJpeg = 0L
    private var control = false
    private var viewers = 0

    private val idleStop = Runnable { stop() }
    private val controlWatch = object : Runnable {
        override fun run() {
            val now = MirrorInput.available()
            if (now != control) { control = now; sendMeta() }
            h.postDelayed(this, 2_000)
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        Prefs.init(this)
        thread = HandlerThread("nocap-mirror").apply { start() }
        h = Handler(thread.looper)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) { stop(); return START_NOT_STICKY }
        val code = intent?.getIntExtra(CODE, 0) ?: 0
        val data = intent?.let { IntentCompat.getParcelableExtra(it, DATA, Intent::class.java) }
        val link = runCatching { Vault.get(this).link }.getOrNull()
        // Android 14: the foreground service (type mediaProjection) comes first.
        val ok = runCatching {
            ServiceCompat.startForeground(
                this, NOTIF, notification(link),
                if (Build.VERSION.SDK_INT >= 29) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION else 0,
            )
        }.isSuccess
        if (!ok || data == null || link == null) {
            MirrorAsk.postState(this, "unsupported", intent?.getStringExtra(ASK))
            stopSelf()
            return START_NOT_STICKY
        }
        val newAsk = intent.getStringExtra(ASK)
        val p = runCatching { getSystemService(MediaProjectionManager::class.java)?.getMediaProjection(code, data) }.getOrNull()
        if (p == null) {
            MirrorAsk.postState(this, "unsupported", newAsk)
            stop()
            return START_NOT_STICKY
        }
        running = true
        h.post {
            if (projection != null) teardown(sendStopped = false) // a fresh OK replaces the session
            stopping = false
            failures = 0
            this.link = link
            this.ask = newAsk
            projection = p
            val cb = object : MediaProjection.Callback() {
                override fun onStop() { if (projection === p) stop() }
            }
            projCb = cb
            p.registerCallback(cb, h)
            startPipeline()
            connect()
            h.postDelayed(idleStop, IDLE_MS)
            h.post(controlWatch)
            getSystemService(DisplayManager::class.java)?.registerDisplayListener(displays, h)
        }
        return START_NOT_STICKY
    }

    private val displays = object : DisplayManager.DisplayListener {
        override fun onDisplayAdded(displayId: Int) {}
        override fun onDisplayRemoved(displayId: Int) {}
        override fun onDisplayChanged(displayId: Int) {
            if (displayId != Display.DEFAULT_DISPLAY || projection == null) return
            // Rotated (or resized): restart the encoder at the new size.
            if (MirrorInput.screenSize(this@MirrorService) != screen || rotation() != rot) startPipeline()
        }
    }

    @Suppress("DEPRECATION")
    private fun rotation(): Int = getSystemService(DisplayManager::class.java)?.getDisplay(Display.DEFAULT_DISPLAY)?.rotation ?: 0

    // ---------- capture ----------

    /** (Re)starts the encoder or the picture reader at the screen's current size. Runs on [h]. */
    private fun startPipeline() {
        val p = projection ?: return
        stopEncoder()
        screen = MirrorInput.screenSize(this)
        rot = rotation()
        val (sw, sh) = screen
        val dpi = resources.displayMetrics.densityDpi
        val surface: Surface
        if (jpeg) {
            val (jw, jh) = Protocol.fit(sw, sh, JPEG_LONG)
            w = jw; h0 = jh
            val r = ImageReader.newInstance(jw, jh, PixelFormat.RGBA_8888, 2)
            r.setOnImageAvailableListener({ onImage(it) }, h)
            reader = r
            surface = r.surface
        } else {
            val (vw, vh) = Protocol.fit(sw, sh, VIDEO_LONG)
            w = vw; h0 = vh
            val c = try {
                encoder(vw, vh)
            } catch (e: Exception) {
                com.nocap.link.system.CrashLog.warn("mirror encoder", e)
                // No usable H.264 encoder: pictures instead.
                jpeg = true
                startPipeline()
                return
            }
            codec = c
            surface = c.createInputSurface()
            c.start()
        }
        val d = display
        if (d == null) {
            // Android 14 allows one virtual display per consent: later sizes resize it.
            display = runCatching {
                p.createVirtualDisplay("NoCap mirror", w, h0, dpi, DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, surface, null, h)
            }.getOrElse {
                com.nocap.link.system.CrashLog.warn("mirror display", it)
                MirrorAsk.postState(this, "unsupported", ask)
                stop()
                return
            }
        } else {
            d.surface = null
            d.resize(w, h0, dpi)
            d.surface = surface
        }
        needKey = false
        sendMeta()
    }

    private fun encoder(vw: Int, vh: Int): MediaCodec {
        val f = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, vw, vh).apply {
            setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
            setInteger(MediaFormat.KEY_BIT_RATE, BITRATE)
            setInteger(MediaFormat.KEY_FRAME_RATE, FPS)
            setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 2)
            // A still screen still sends a picture now and then, so new windows see something.
            setLong(MediaFormat.KEY_REPEAT_PREVIOUS_FRAME_AFTER, 100_000L)
            setInteger(MediaFormat.KEY_PRIORITY, 0) // real time
            if (Build.VERSION.SDK_INT >= 30) setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
        }
        val c = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
        try {
            c.setCallback(object : MediaCodec.Callback() {
                override fun onInputBufferAvailable(codec: MediaCodec, index: Int) {}
                override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) {}
                override fun onError(codec: MediaCodec, e: MediaCodec.CodecException) {
                    com.nocap.link.system.CrashLog.warn("mirror codec", e)
                    if (codec === this@MirrorService.codec) h.post { startPipeline() }
                }
                override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
                    if (codec !== this@MirrorService.codec) return
                    try {
                        val buf = codec.getOutputBuffer(index)
                        if (buf != null && info.size > 0 && info.size + Protocol.HEADER <= Protocol.MAX_FRAME) {
                            val type = when {
                                info.flags and MediaCodec.BUFFER_FLAG_CODEC_CONFIG != 0 -> Protocol.TYPE_CONFIG
                                info.flags and MediaCodec.BUFFER_FLAG_KEY_FRAME != 0 -> Protocol.TYPE_KEY
                                else -> Protocol.TYPE_DELTA
                            }
                            buf.position(info.offset)
                            buf.limit(info.offset + info.size)
                            val frame = Protocol.frame(type, info.presentationTimeUs, info.size) { buf.get(it, Protocol.HEADER, info.size) }
                            if (type == Protocol.TYPE_CONFIG) config = frame
                            sendFrame(type, frame)
                        }
                    } catch (_: IllegalStateException) {
                        // Stopped while this buffer was on its way.
                    } finally {
                        runCatching { codec.releaseOutputBuffer(index, false) }
                    }
                }
            }, h)
            c.configure(f, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
        } catch (e: Exception) {
            c.release()
            throw e
        }
        return c
    }

    private fun onImage(r: ImageReader) {
        val img = runCatching { r.acquireLatestImage() }.getOrNull() ?: return
        try {
            if (r !== reader) return
            val now = System.nanoTime()
            if (now - lastJpeg < JPEG_EVERY_NS) return
            val socket = ws ?: return
            if (viewers == 0 || socket.queueSize() > BACKLOG) return
            lastJpeg = now
            val plane = img.planes[0]
            val px = plane.pixelStride
            val rowW = plane.rowStride / px
            val bmp = Bitmap.createBitmap(rowW, img.height, Bitmap.Config.ARGB_8888)
            bmp.copyPixelsFromBuffer(plane.buffer)
            val crop = if (rowW != img.width) Bitmap.createBitmap(bmp, 0, 0, img.width, img.height) else bmp
            val out = ByteArrayOutputStream(64 * 1024)
            crop.compress(Bitmap.CompressFormat.JPEG, JPEG_QUALITY, out)
            if (crop !== bmp) crop.recycle()
            bmp.recycle()
            val bytes = out.toByteArray()
            if (bytes.size + Protocol.HEADER <= Protocol.MAX_FRAME) {
                socket.send(Protocol.frame(Protocol.TYPE_JPEG, img.timestamp / 1000, bytes).toByteString())
            }
        } catch (e: Exception) {
            com.nocap.link.system.CrashLog.warn("mirror jpeg", e)
        } finally {
            img.close()
        }
    }

    /** Sends one encoded frame; a backed-up socket skips ahead to the next key frame. */
    private fun sendFrame(type: Byte, frame: ByteArray) {
        val socket = ws ?: run { needKey = true; return }
        val q = socket.queueSize()
        if (type == Protocol.TYPE_DELTA && (needKey || q > BACKLOG)) {
            if (!needKey) needKey = true
            if (q < BACKLOG / 4) requestKey()
            return
        }
        if (!socket.send(frame.toByteString())) { needKey = true; return }
        if (type == Protocol.TYPE_KEY) needKey = false
    }

    private var lastKeyAsk = 0L

    /** Asks the encoder for a key frame (at most every 500 ms), its settings first. */
    private fun requestKey() {
        val now = System.nanoTime()
        if (now - lastKeyAsk < 500_000_000L) return
        lastKeyAsk = now
        val c = codec ?: return
        config?.let { cfg -> ws?.send(cfg.toByteString()) }
        runCatching { c.setParameters(Bundle().apply { putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0) }) }
    }

    private fun stopEncoder() {
        val c = codec
        codec = null
        if (c != null) { runCatching { c.stop() }; runCatching { c.release() } }
        reader?.let { r -> reader = null; r.setOnImageAvailableListener(null, null); runCatching { r.close() } }
        config = null
    }

    private fun sendMeta() {
        control = MirrorInput.available()
        ws?.send(Protocol.meta(w, h0, rot, jpeg, if (jpeg) 0 else BITRATE, if (jpeg) JPEG_FPS else FPS, control))
    }

    // ---------- the socket ----------

    private fun connect() {
        val l = link ?: return
        if (stopping) return
        val c = client ?: Pins.client(l.pin).newBuilder()
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .pingInterval(15, TimeUnit.SECONDS)
            .build().also { client = it }
        val url = l.server + "/api/v1/device/mirror" + (ask?.let { "?ask=" + URLEncoder.encode(it, "UTF-8") } ?: "")
        val req = Request.Builder().url(url).header("Authorization", "Device ${l.token}").build()
        val gen = ++wsGen
        ws = c.newWebSocket(req, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                h.post {
                    if (gen != wsGen) return@post
                    failures = 0
                    webSocket.send(Protocol.state("streaming"))
                    sendMeta()
                    needKey = true
                    requestKey()
                }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                val m = Protocol.parse(text) ?: return
                h.post { if (gen == wsGen) control(m) }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {}

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(1000, null)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                h.post { if (gen == wsGen) lost(code == 4401 || code == 1008) }
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                val gone = response?.code == 401 || response?.code == 403 || response?.code == 404
                h.post { if (gen == wsGen) lost(gone) }
            }
        })
    }

    /** The socket dropped: try again a few times, then give up. */
    private fun lost(forGood: Boolean) {
        ws = null
        if (stopping) return
        failures++
        if (forGood || failures > MAX_RETRIES) { stop(); return }
        h.postDelayed({ connect() }, (1_000L shl (failures - 1)).coerceAtMost(8_000L))
    }

    private fun control(m: Protocol.Control) {
        when (m) {
            is Protocol.Control.Viewers -> {
                viewers = m.n
                h.removeCallbacks(idleStop)
                if (m.n == 0) h.postDelayed(idleStop, IDLE_MS)
            }
            is Protocol.Control.Mode -> if (m.jpeg != jpeg) { jpeg = m.jpeg; startPipeline() }
            is Protocol.Control.KeyFrame -> { needKey = true; lastKeyAsk = 0; requestKey() }
            else -> main.post { MirrorInput.handle(this, m) }
        }
    }

    // ---------- ending ----------

    private fun stop() {
        if (Looper.myLooper() != h.looper) { h.post { stop() }; return }
        if (stopping) return
        stopping = true
        teardown(sendStopped = true)
        running = false
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    /** Ends capture and the socket. Runs on [h]. */
    private fun teardown(sendStopped: Boolean) {
        h.removeCallbacks(idleStop)
        h.removeCallbacks(controlWatch)
        runCatching { getSystemService(DisplayManager::class.java)?.unregisterDisplayListener(displays) }
        wsGen++
        ws?.let { s -> runCatching { s.send(Protocol.state("stopping")); s.close(1000, "stopped") } }
        ws = null
        stopEncoder()
        display?.let { runCatching { it.release() } }
        display = null
        val p = projection
        projection = null
        if (p != null) {
            projCb?.let { cb -> runCatching { p.unregisterCallback(cb) } }
            runCatching { p.stop() }
        }
        projCb = null
        if (sendStopped) MirrorAsk.postState(this, "stopped", ask)
    }

    override fun onDestroy() {
        if (!stopping) {
            stopping = true
            h.post { teardown(sendStopped = true) }
        }
        running = false
        thread.quitSafely()
        super.onDestroy()
    }

    private fun notification(link: Link?): Notification {
        getSystemService(NotificationManager::class.java)?.createNotificationChannel(
            NotificationChannel(CHANNEL, t("Screen mirroring", "Screen mirroring"), NotificationManager.IMPORTANCE_LOW).apply {
                description = t("Shows while your screen is on NoCap OS", "Shown while this phone's screen is mirrored to NoCap OS")
            },
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, MirrorService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val server = MirrorAsk.serverName(link)
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setContentTitle(t("Your screen is being mirrored", "Your screen is being mirrored"))
            .setContentText(t("Live on NoCap OS ($server). Hit Stop to end it.", "Shown on NoCap OS ($server). Tap Stop to end it."))
            .setOngoing(true)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .addAction(0, t("Stop", "Stop"), stop)
            .build()
    }

    companion object {
        const val CODE = "code"
        const val DATA = "data"
        const val ASK = "ask"
        const val ACTION_STOP = "com.nocap.link.mirror.STOP"
        private const val CHANNEL = "nocap_mirror_live"
        private const val NOTIF = 4201
        private const val VIDEO_LONG = 1080
        private const val JPEG_LONG = 720
        private const val BITRATE = 6_000_000
        private const val FPS = 30
        private const val JPEG_FPS = 10
        private const val JPEG_QUALITY = 60
        private const val JPEG_EVERY_NS = 1_000_000_000L / JPEG_FPS
        /** Bytes waiting in the socket before frames are skipped. */
        private const val BACKLOG = 2L shl 20
        /** Nobody watching for this long: stop. */
        private const val IDLE_MS = 30_000L
        private const val MAX_RETRIES = 4

        /** Whether a mirroring session is on. */
        @Volatile var running = false
            private set

        fun stop(ctx: Context) {
            if (!running) return
            runCatching { ctx.startService(Intent(ctx, MirrorService::class.java).setAction(ACTION_STOP)) }
        }
    }
}

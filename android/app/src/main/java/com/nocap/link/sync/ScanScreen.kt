package com.nocap.link.sync

import android.content.pm.PackageManager
import android.Manifest
import android.net.Uri
import android.os.Build
import android.view.HapticFeedbackConstants
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.Camera
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.RoundRect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import com.nocap.link.data.t
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/** Whether a scanned code is a NoCap Link pairing code (nocap://pair?…). */
fun isPairCode(text: String?): Boolean {
    val s = text?.trim() ?: return false
    return s.startsWith("nocap://pair?", ignoreCase = true) && s.length > "nocap://pair?".length
}

/** The viewfinder's square, as a fraction of the screen's shorter side. */
private const val FINDER = 0.68f

/**
 * Scans the NoCap Link QR (nocap://pair?…) full screen, in NoCap style: red
 * rounded corner brackets with a moving scan line, the rest of the picture
 * dimmed, a torch and a way to type the code instead. The camera is asked for
 * only here, the picture is read on the phone (ZXing, on-device) and nothing
 * is recorded or sent anywhere.
 */
@Composable
fun ScanScreen(onResult: (Uri) -> Unit, onCancel: () -> Unit, onType: () -> Unit = onCancel) {
    val ctx = LocalContext.current
    val hostView = LocalView.current
    var allowed by remember { mutableStateOf(ContextCompat.checkSelfPermission(ctx, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) }
    var asked by remember { mutableStateOf(false) }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { allowed = it; asked = true }
    var camError by remember { mutableStateOf<String?>(null) }
    var camera by remember { mutableStateOf<Camera?>(null) }
    var torch by remember { mutableStateOf(false) }
    var found by remember { mutableStateOf(false) }
    // Asked when the scanner opens, never before.
    LaunchedEffect(Unit) { if (!allowed) runCatching { ask.launch(Manifest.permission.CAMERA) } }

    val live = allowed && camError == null
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        if (live) {
            CameraScanner(
                onCamera = { camera = it },
                onResult = { uri ->
                    found = true
                    // A short buzz: got it.
                    hostView.performHapticFeedback(if (Build.VERSION.SDK_INT >= 30) HapticFeedbackConstants.CONFIRM else HapticFeedbackConstants.LONG_PRESS)
                    onResult(uri)
                },
                onError = { camError = it },
            )
            Viewfinder(found)
        }

        Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().padding(20.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                RoundIcon(t("Close", "Close"), onCancel) { Icon(Icons.Filled.Close, contentDescription = null, tint = Color.White) }
                Spacer(Modifier.weight(1f))
                if (live && camera?.cameraInfo?.hasFlashUnit() == true) {
                    RoundIcon(if (torch) t("Torch off", "Turn the torch off") else t("Torch on", "Turn the torch on"), {
                        torch = !torch
                        camera?.cameraControl?.enableTorch(torch)
                    }, on = torch) { TorchGlyph(if (torch) Color.Black else Color.White) }
                }
            }
            Spacer(Modifier.weight(1f))
            Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text(
                    t("SCAN TO LINK", "SCAN TO LINK"),
                    style = MaterialTheme.typography.headlineSmall, color = Color.White, textAlign = TextAlign.Center,
                )
                Text(
                    camError ?: when {
                        allowed -> t(
                            "Point at the QR in Settings → Your Squad → Add to the squad on your NoCap server.",
                            "Point the camera at the QR code in Settings → Devices → Link a device on your NoCap server.",
                        )
                        asked -> t(
                            "No camera, no scan, all good. Type the code instead, or allow the camera in the phone's settings.",
                            "Without the camera you can type the code instead, or allow the camera in the phone's settings.",
                        )
                        else -> t(
                            "The camera only reads the QR, right here on the phone. Nothing gets recorded or uploaded.",
                            "The camera is only used to read the QR code on this phone. Nothing is recorded or uploaded.",
                        )
                    },
                    style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.78f), textAlign = TextAlign.Center,
                )
                if (!allowed) {
                    RedButton(t("Allow camera", "Allow camera"), { ask.launch(Manifest.permission.CAMERA) }, Modifier.fillMaxWidth())
                }
                Box(
                    Modifier.clip(RoundedCornerShape(999.dp))
                        .background(Color.White.copy(alpha = 0.14f))
                        .border(1.dp, Color.White.copy(alpha = 0.22f), RoundedCornerShape(999.dp))
                        .clickable(onClick = onType)
                        .padding(horizontal = 20.dp, vertical = 11.dp),
                ) {
                    Text(t("Type the code instead", "Type the code instead"), style = MaterialTheme.typography.labelLarge, color = Color.White)
                }
            }
        }
    }
}

/** A round glass button for the top bar. */
@Composable
private fun RoundIcon(label: String, onClick: () -> Unit, on: Boolean = false, content: @Composable () -> Unit) {
    Box(
        Modifier.size(46.dp).clip(CircleShape)
            .background(if (on) Color.White else Color.Black.copy(alpha = 0.45f))
            .border(1.dp, Color.White.copy(alpha = 0.25f), CircleShape)
            .clickable(onClick = onClick)
            .semantics { contentDescription = label },
        contentAlignment = Alignment.Center,
    ) { content() }
}

/** A lightning bolt, for the torch. */
@Composable
private fun TorchGlyph(color: Color) {
    Canvas(Modifier.size(20.dp)) {
        val w = size.width
        val h = size.height
        val p = Path().apply {
            moveTo(w * 0.58f, 0f)
            lineTo(w * 0.16f, h * 0.58f)
            lineTo(w * 0.48f, h * 0.58f)
            lineTo(w * 0.40f, h)
            lineTo(w * 0.84f, h * 0.40f)
            lineTo(w * 0.52f, h * 0.40f)
            close()
        }
        drawPath(p, color)
    }
}

/** The dimmed screen with a clear rounded window, red corner brackets and a moving scan line. */
@Composable
private fun Viewfinder(found: Boolean) {
    val anim = rememberInfiniteTransition(label = "scan")
    val y by anim.animateFloat(0.04f, 0.96f, infiniteRepeatable(tween(1800, easing = FastOutSlowInEasing), RepeatMode.Reverse), label = "line")
    Canvas(Modifier.fillMaxSize()) {
        val side = minOf(size.width, size.height) * FINDER
        val left = (size.width - side) / 2
        val top = (size.height - side) / 2 - size.height * 0.06f
        val radius = side * 0.11f
        val window = RoundRect(Rect(Offset(left, top), Size(side, side)), CornerRadius(radius))

        // Everything but the window, dimmed.
        val dim = Path().apply {
            fillType = PathFillType.EvenOdd
            addRect(Rect(Offset.Zero, size))
            addRoundRect(window)
        }
        drawPath(dim, Color.Black.copy(alpha = 0.58f))

        // Corner brackets: rounded, in NoCap red.
        val stroke = Stroke(width = 5.dp.toPx(), cap = StrokeCap.Round)
        val arm = side * 0.2f
        val k = radius * 0.45f
        fun corner(x: Float, y0: Float, sx: Float, sy: Float) {
            val p = Path().apply {
                moveTo(x, y0 + sy * arm)
                lineTo(x, y0 + sy * radius)
                cubicTo(x, y0 + sy * k, x + sx * k, y0, x + sx * radius, y0)
                lineTo(x + sx * arm, y0)
            }
            drawPath(p, NoCap.Red, style = stroke)
        }
        corner(left, top, 1f, 1f)
        corner(left + side, top, -1f, 1f)
        corner(left, top + side, 1f, -1f)
        corner(left + side, top + side, -1f, -1f)

        // The scan line, fading out at both ends.
        if (!found) {
            val ly = top + side * y
            val inset = side * 0.08f
            drawLine(
                Brush.horizontalGradient(listOf(Color.Transparent, NoCap.Red, Color.Transparent), startX = left + inset, endX = left + side - inset),
                Offset(left + inset, ly), Offset(left + side - inset, ly), strokeWidth = 3.dp.toPx(), cap = StrokeCap.Round,
            )
            drawRect(
                Brush.verticalGradient(listOf(Color.Transparent, NoCap.Red.copy(alpha = 0.18f)), startY = ly - side * 0.12f, endY = ly),
                Offset(left + inset, ly - side * 0.12f), Size(side - 2 * inset, side * 0.12f),
            )
        } else {
            drawRoundRect(NoCap.Red.copy(alpha = 0.16f), Offset(left, top), Size(side, side), CornerRadius(radius))
        }
    }
}

@Composable
private fun CameraScanner(onCamera: (Camera) -> Unit, onResult: (Uri) -> Unit, onError: (String) -> Unit) {
    val ctx = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val done = remember { AtomicBoolean(false) }
    val view = remember { PreviewView(ctx).apply { scaleType = PreviewView.ScaleType.FILL_CENTER } }

    DisposableEffect(owner) {
        val exec = Executors.newSingleThreadExecutor()
        val disposed = AtomicBoolean(false)
        var analysis: ImageAnalysis? = null
        val future = ProcessCameraProvider.getInstance(ctx)
        future.addListener({
            if (disposed.get()) return@addListener // left the screen before the camera was ready
            try {
                // Checked right before use: the camera permission can be revoked at any time.
                if (ContextCompat.checkSelfPermission(ctx, Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
                    onError(t("Camera's off for NoCap OS now. Type the code instead.", "Camera access is off. Type the code instead."))
                    return@addListener
                }
                val provider = future.get()
                val preview = Preview.Builder().build().also { it.setSurfaceProvider(view.surfaceProvider) }
                val reader = MultiFormatReader().apply {
                    setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE), DecodeHintType.TRY_HARDER to true))
                }
                val a = ImageAnalysis.Builder().setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST).build()
                a.setAnalyzer(exec) { img ->
                    // decode() always closes the frame.
                    val text = decode(img, reader)
                    // Only pairing codes stop the scan; any other code keeps it going.
                    if (isPairCode(text) && done.compareAndSet(false, true)) {
                        val uri = runCatching { Uri.parse(text!!.trim()) }.getOrNull()
                        ContextCompat.getMainExecutor(ctx).execute { if (!disposed.get() && uri != null) onResult(uri) }
                    }
                }
                analysis = a
                provider.unbindAll()
                onCamera(provider.bindToLifecycle(owner, CameraSelector.DEFAULT_BACK_CAMERA, preview, a))
            } catch (_: Exception) {
                // No back camera, camera in use by another app, or the camera service failed.
                onError(t("Camera's busy or missing rn. Type the code instead.", "Can't open the camera (it may be in use). Type the code instead."))
            }
        }, ContextCompat.getMainExecutor(ctx))
        onDispose {
            disposed.set(true)
            analysis?.clearAnalyzer()
            // Never block the main thread waiting for the camera to start.
            if (future.isDone) runCatching { future.get().unbindAll() }
            exec.shutdown()
        }
    }
    AndroidView({ view }, Modifier.fillMaxSize())
}

private fun decode(img: ImageProxy, reader: MultiFormatReader): String? {
    try {
        val plane = img.planes[0]
        val buf = plane.buffer
        val data = ByteArray(buf.remaining()).also { buf.get(it) }
        val src = PlanarYUVLuminanceSource(data, plane.rowStride, img.height, 0, 0, img.width, img.height, false)
        return reader.decodeWithState(BinaryBitmap(HybridBinarizer(src)))?.text
    } catch (_: NotFoundException) {
        return null
    } catch (_: Exception) {
        return null
    } finally {
        reader.reset()
        img.close()
    }
}

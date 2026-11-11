package com.nocap.link.sync

import android.content.pm.PackageManager
import android.Manifest
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.Icons
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Wallpaper
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.Executors

/** Scans the NoCap Link QR (nocap://pair?…) on the phone, no cloud involved. */
@Composable
fun ScanScreen(onResult: (Uri) -> Unit, onCancel: () -> Unit) {
    val ctx = LocalContext.current
    var allowed by remember { mutableStateOf(ContextCompat.checkSelfPermission(ctx, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) }
    var asked by remember { mutableStateOf(false) }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { allowed = it; asked = true }
    LaunchedEffect(Unit) { if (!allowed) ask.launch(Manifest.permission.CAMERA) }

    Wallpaper {
        if (allowed) {
            CameraScanner(onResult)
            Box(Modifier.align(Alignment.Center).size(250.dp).border(3.dp, NoCap.Red, RoundedCornerShape(28.dp)))
        }
        Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().padding(20.dp)) {
            IconButton(onClick = onCancel) { Icon(Icons.Filled.Close, contentDescription = "Close", tint = NoCap.Text) }
            Spacer(Modifier.weight(1f))
            GlassCard(Modifier.fillMaxWidth()) {
                Column {
                    Text("SCAN TO LINK", style = MaterialTheme.typography.headlineSmall)
                    Spacer(Modifier.height(6.dp))
                    Text(
                        if (allowed) "Point at the QR in Settings → Devices → Link a device on your NoCap desktop."
                        else if (asked) "No camera, no scan — that's okay. Go back and type the address and code instead, or allow the camera in Settings."
                        else "The camera is only used to read the QR code. Nothing gets recorded or uploaded.",
                        style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, textAlign = TextAlign.Start,
                    )
                    if (!allowed) {
                        Spacer(Modifier.height(12.dp))
                        RedButton("Allow camera", { ask.launch(Manifest.permission.CAMERA) }, Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}

@Composable
private fun CameraScanner(onResult: (Uri) -> Unit) {
    val ctx = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val done = remember { AtomicBoolean(false) }
    val exec = remember { Executors.newSingleThreadExecutor() }
    val view = remember { PreviewView(ctx).apply { scaleType = PreviewView.ScaleType.FILL_CENTER } }

    DisposableEffect(owner) {
        val future = ProcessCameraProvider.getInstance(ctx)
        future.addListener({
            val provider = future.get()
            val preview = Preview.Builder().build().also { it.setSurfaceProvider(view.surfaceProvider) }
            val reader = MultiFormatReader().apply {
                setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE), DecodeHintType.TRY_HARDER to true))
            }
            val analysis = ImageAnalysis.Builder().setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST).build()
            analysis.setAnalyzer(exec) { img -> decode(img, reader)?.let { text ->
                val uri = Uri.parse(text)
                if (uri.scheme == "nocap" && done.compareAndSet(false, true)) {
                    ContextCompat.getMainExecutor(ctx).execute { onResult(uri) }
                }
            } }
            runCatching {
                provider.unbindAll()
                provider.bindToLifecycle(owner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
            }
        }, ContextCompat.getMainExecutor(ctx))
        onDispose {
            runCatching { future.get().unbindAll() }
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

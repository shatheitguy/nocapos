package com.nocap.link.net

import android.net.http.SslCertificate
import android.os.Build
import java.io.ByteArrayInputStream
import java.security.cert.CertificateException
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager
import okhttp3.OkHttpClient

/**
 * Trust on first use. NoCapOS servers usually have a self-signed
 * certificate, so the first time you link, the app shows the certificate's
 * SHA-256 and you OK it. After that only that exact certificate is
 * accepted: by the API client and by the desktop web view.
 */
object Pins {
    /** SHA-256 of a certificate, lowercase hex. */
    fun sha256(cert: X509Certificate): String =
        MessageDigest.getInstance("SHA-256").digest(cert.encoded).joinToString("") { "%02x".format(it) }

    /** "AB:CD:…" grouped for people to read. */
    fun pretty(pin: String): String = pin.uppercase().chunked(2).joinToString(":")

    /** The leaf certificate behind a web view's SslCertificate. */
    fun x509(cert: SslCertificate?): X509Certificate? {
        cert ?: return null
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) return cert.x509Certificate
        val bytes = SslCertificate.saveState(cert)?.getByteArray("x509-certificate") ?: return null
        return runCatching {
            CertificateFactory.getInstance("X.509").generateCertificate(ByteArrayInputStream(bytes)) as X509Certificate
        }.getOrNull()
    }

    /** Thrown when the server shows a different certificate than the pinned one. */
    class PinMismatch(val seen: String) : CertificateException("certificate changed")

    /** Accepts exactly one leaf certificate (or, with pin = null, records any). */
    class PinTrust(private val pin: String?) : X509TrustManager {
        @Volatile var seen: String? = null
            private set

        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
            val leaf = chain?.firstOrNull() ?: throw CertificateException("no certificate")
            val got = sha256(leaf)
            seen = got
            if (pin != null && !got.equals(pin, ignoreCase = true)) throw PinMismatch(got)
        }

        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) =
            throw CertificateException("not a server")

        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
    }

    /**
     * An HTTP client for one server. With a pin, only that certificate
     * works (whatever the host name, since self-signed certificates rarely
     * match a LAN address). Without one (probe), any certificate is
     * recorded in [PinTrust.seen] so it can be shown to the person.
     */
    fun client(pin: String?): OkHttpClient = synchronized(clients) {
        // One client per certificate, sharing threads and connections:
        // every NoCapApi used to build its own, leaking a thread pool each.
        clients.getOrPut(pin ?: "") { build(PinTrust(pin)) }
    }

    /** A fresh client recording the certificate it sees (probing a new server). */
    fun client(pin: String?, trust: PinTrust): OkHttpClient = build(trust)

    private val clients = HashMap<String, OkHttpClient>()

    /** Threads and the connection pool, shared by every client above. */
    private val base: OkHttpClient by lazy { OkHttpClient() }

    private fun build(trust: PinTrust): OkHttpClient {
        val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), null) }
        return base.newBuilder()
            .sslSocketFactory(ctx.socketFactory, trust)
            .hostnameVerifier { _, _ -> true } // the certificate itself is pinned above
            .connectTimeout(8, TimeUnit.SECONDS)
            .readTimeout(20, TimeUnit.SECONDS)
            .writeTimeout(20, TimeUnit.SECONDS)
            .followRedirects(false)
            .build()
    }
}

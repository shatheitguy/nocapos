import java.util.Base64

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

// Release signing comes from the environment (GitHub secrets in CI):
//   NOCAP_KEYSTORE_B64       the keystore (.jks), base64-encoded
//   NOCAP_KEYSTORE_PASSWORD  its password
//   NOCAP_KEY_ALIAS          the key's alias
//   NOCAP_KEY_PASSWORD       the key's password
// Without them, release builds are signed with the debug key so CI still works.
val releaseKeystore: File? = System.getenv("NOCAP_KEYSTORE_B64")?.takeIf { it.isNotBlank() }?.let { b64 ->
    layout.buildDirectory.file("signing/nocap-release.jks").get().asFile.apply {
        parentFile.mkdirs()
        writeBytes(Base64.getMimeDecoder().decode(b64.trim()))
    }
}
val hasReleaseKey = releaseKeystore != null &&
    listOf("NOCAP_KEYSTORE_PASSWORD", "NOCAP_KEY_ALIAS", "NOCAP_KEY_PASSWORD").all { !System.getenv(it).isNullOrBlank() }

android {
    namespace = "com.nocap.link"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.nocap.link"
        minSdk = 26
        targetSdk = 34
        versionCode = 4
        versionName = "3.1.0"
    }

    signingConfigs {
        if (hasReleaseKey) {
            create("release") {
                storeFile = releaseKeystore
                storePassword = System.getenv("NOCAP_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("NOCAP_KEY_ALIAS")
                keyPassword = System.getenv("NOCAP_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            // No shrinking: the app is small, and reflection (status bar, widgets) stays safe.
            isMinifyEnabled = false
            signingConfig = if (hasReleaseKey) signingConfigs.getByName("release") else signingConfigs.getByName("debug")
        }
    }

    lint {
        // Real errors fail the build; what was already there is listed in lint-baseline.xml.
        baseline = file("lint-baseline.xml")
        abortOnError = true
        checkReleaseBuilds = true
        warningsAsErrors = false
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    packaging {
        resources {
            excludes += setOf("META-INF/{AL2.0,LGPL2.1}", "META-INF/versions/**")
        }
    }
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2024.09.03"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-core")

    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.6")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // Device token + pinned certificate, encrypted at rest.
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
    // Heartbeat every 15 minutes.
    implementation("androidx.work:work-runtime-ktx:2.9.1")
    // HTTPS with a trust-on-first-use certificate pin.
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // QR scanning: camera preview + an on-device decoder (no cloud services).
    implementation("androidx.camera:camera-camera2:1.3.4")
    implementation("androidx.camera:camera-lifecycle:1.3.4")
    implementation("androidx.camera:camera-view:1.3.4")
    implementation("com.google.zxing:core:3.5.3")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}

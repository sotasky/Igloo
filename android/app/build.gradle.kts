plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
    id("com.google.devtools.ksp")
}

fun String.asBuildConfigString(): String =
    replace("\\", "\\\\").replace("\"", "\\\"")

val releaseStoreFilePath = providers.environmentVariable("ANDROID_KEYSTORE_PATH").orNull
    ?: providers.gradleProperty("ANDROID_KEYSTORE_PATH").orNull
val releaseStorePassword = providers.environmentVariable("ANDROID_KEYSTORE_PASSWORD").orNull
    ?: providers.gradleProperty("ANDROID_KEYSTORE_PASSWORD").orNull
val releaseKeyAlias = providers.environmentVariable("ANDROID_KEY_ALIAS").orNull
    ?: providers.gradleProperty("ANDROID_KEY_ALIAS").orNull
val releaseKeyPassword = providers.environmentVariable("ANDROID_KEY_PASSWORD").orNull
    ?: providers.gradleProperty("ANDROID_KEY_PASSWORD").orNull
val hasReleaseSigning = !releaseStoreFilePath.isNullOrBlank() &&
    !releaseStorePassword.isNullOrBlank() &&
    !releaseKeyAlias.isNullOrBlank() &&
    !releaseKeyPassword.isNullOrBlank()

android {
    namespace = "com.screwy.igloo"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.screwy.igloo"
        minSdk = 26
        targetSdk = 36
        // versionCode continues past the legacy v1 install (which was 2) for a monotonic
        // history. Android uses a fresh per-machine debug keystore (AGP default), so installing
        // over v1 needs an explicit uninstall once — the signatures don't match on purpose.
        versionCode = 38
        versionName = "2.0.23"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        vectorDrawables { useSupportLibrary = true }

        val serverUrl = (project.findProperty("DEFAULT_SERVER_URL")?.toString() ?: "").trim()
        buildConfigField("String", "DEFAULT_SERVER_URL", "\"${serverUrl.asBuildConfigString()}\"")
    }

    // Debug signing uses AGP's default per-machine keystore (~/.android/debug.keystore).
    // Release signing is wired per-machine when a release APK is actually built — credentials
    // live outside the repo (env vars or a gitignored ~/.gradle/gradle.properties entry).
    signingConfigs {
        if (hasReleaseSigning) {
            create("release") {
                storeFile = file(releaseStoreFilePath!!)
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }
    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            if (hasReleaseSigning) {
                signingConfig = signingConfigs.getByName("release")
            }
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
        create("devtest") {
            initWith(getByName("debug"))
            applicationIdSuffix = ".devtest"
            versionNameSuffix = "-devtest"
            matchingFallbacks += listOf("debug")
        }
    }
    testBuildType = "devtest"

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    packaging {
        jniLibs {
            keepDebugSymbols += "**/libandroidx.graphics.path.so"
        }
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
            excludes += "/META-INF/LICENSE.md"
            excludes += "/META-INF/LICENSE-notice.md"
            excludes += "/META-INF/services/javax.script.ScriptEngineFactory"
        }
    }
    testOptions {
        unitTests.isReturnDefaultValues = true
        // Robolectric (room-testing JVM runner) needs Android resources on the test
        // classpath to resolve `ApplicationProvider.getApplicationContext()`.
        unitTests.isIncludeAndroidResources = true
        unitTests.all {
            it.jvmArgs("--enable-native-access=ALL-UNNAMED")
        }
        managedDevices {
            localDevices {
                create("pixel2Api35") {
                    device = "Pixel 2"
                    apiLevel = 35
                    systemImageSource = "aosp-atd"
                    testedAbi = "x86_64"
                }
            }
        }
    }
    sourceSets.getByName("devtest").assets.directories.add("$projectDir/schemas")
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

ksp {
    // Room schema export for the current app schema.
    arg("room.schemaLocation", "$projectDir/schemas")
}

val roomVersion = "2.8.4"
val ktorVersion = "3.5.0"
val lifecycleVersion = "2.10.0"
val koinVersion = "4.2.1"
val coilVersion = "3.4.0"
val media3Version = "1.10.1"
val asmVersion = "9.10"

dependencies {
    // Core Android
    implementation("androidx.core:core-ktx:1.18.0")
    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.recyclerview:recyclerview:1.4.0")
    implementation("androidx.swiperefreshlayout:swiperefreshlayout:1.2.0")

    // Lifecycle
    implementation("androidx.lifecycle:lifecycle-runtime-compose:$lifecycleVersion")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:$lifecycleVersion")
    implementation("androidx.lifecycle:lifecycle-process:$lifecycleVersion")

    // Compose
    implementation(platform("androidx.compose:compose-bom:2026.05.01"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.navigation:navigation-compose:2.9.8")

    // Room
    implementation("androidx.room:room-runtime:$roomVersion")
    implementation("androidx.room:room-ktx:$roomVersion")
    ksp("androidx.room:room-compiler:$roomVersion")

    // Ktor
    implementation("io.ktor:ktor-client-android:$ktorVersion")
    implementation("io.ktor:ktor-client-content-negotiation:$ktorVersion")
    implementation("io.ktor:ktor-serialization-kotlinx-json:$ktorVersion")

    // kotlinx.serialization
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.11.0")

    // Koin DI
    implementation("io.insert-koin:koin-androidx-compose:$koinVersion")

    // Coil
    implementation("io.coil-kt.coil3:coil-compose:$coilVersion")
    implementation("io.coil-kt.coil3:coil-network-ktor3:$coilVersion")

    // Media3
    implementation("androidx.media3:media3-exoplayer:$media3Version")
    implementation("androidx.media3:media3-ui:$media3Version")
    implementation("androidx.media3:media3-session:$media3Version")

    // WorkManager
    implementation("androidx.work:work-runtime-ktx:2.11.2")

    // Security — EncryptedSharedPreferences (scoped to auth/ for bearer/refresh tokens)
    implementation("androidx.security:security-crypto:1.1.0")

    // Tests
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
    testImplementation("com.squareup.okhttp3:mockwebserver:5.3.2")
    testImplementation("io.ktor:ktor-client-mock:$ktorVersion")
    // Room tests run on JVM via Robolectric — spins up enough Android framework for
    // Room's SQLiteOpenHelper to initialize without a device.
    testImplementation("androidx.room:room-testing:$roomVersion")
    testImplementation("org.robolectric:robolectric:4.16.1")
    // Robolectric 4.16.1 still resolves ASM 9.8; 9.9.x is needed for Java 26 class files.
    testImplementation("org.ow2.asm:asm:$asmVersion")
    testImplementation("org.ow2.asm:asm-commons:$asmVersion")
    testImplementation("org.ow2.asm:asm-tree:$asmVersion")
    testImplementation("androidx.test:core-ktx:1.7.0")
    testImplementation("androidx.test.ext:junit:1.3.0")
    testImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.room:room-testing:$roomVersion")
    androidTestImplementation("androidx.work:work-testing:2.11.2")
    androidTestImplementation("io.insert-koin:koin-test:$koinVersion")
    androidTestImplementation("androidx.test:core-ktx:1.7.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
    androidTestImplementation(platform("androidx.compose:compose-bom:2026.05.01"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("com.squareup.okhttp3:mockwebserver:5.3.2")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
    "devtestImplementation"("androidx.compose.ui:ui-tooling")
    "devtestImplementation"("androidx.compose.ui:ui-test-manifest")
}

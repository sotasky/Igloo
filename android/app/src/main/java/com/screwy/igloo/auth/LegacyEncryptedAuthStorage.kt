@file:Suppress("DEPRECATION")

package com.screwy.igloo.auth

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

/**
 * Read-only bridge for auth data written by the old AndroidX Security storage.
 * New writes go through [KeystoreAuthStorage]; this file is the only place that keeps
 * the deprecated API so installed apps can preserve login state during the upgrade.
 */
internal object LegacyEncryptedAuthStorage {
    fun readKnownValues(context: Context): List<AuthStorageEntry> {
        val prefs = encryptedPrefs(context.applicationContext)
        return authStorageKeySpecs.mapNotNull { spec ->
            if (!prefs.contains(spec.key)) return@mapNotNull null
            when (spec.type) {
                AuthStorageValueType.STRING -> prefs.getString(spec.key, null)
                AuthStorageValueType.LONG -> prefs.getLong(spec.key, 0L).toString()
                AuthStorageValueType.BOOLEAN -> prefs.getBoolean(spec.key, false).toString()
            }?.let { value ->
                AuthStorageEntry(key = spec.key, type = spec.type, value = value)
            }
        }
    }

    private fun encryptedPrefs(context: Context): SharedPreferences {
        val masterKey = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        return EncryptedSharedPreferences.create(
            context,
            LEGACY_PREFS_NAME,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    private const val LEGACY_PREFS_NAME = "igloo-auth"
}

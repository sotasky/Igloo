package com.screwy.igloo.auth

import android.content.Context
import android.content.SharedPreferences
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Log
import java.security.KeyStore
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Keystore-backed [AuthStorage]. Auth values are serialized into one state blob and
 * encrypted with an AES-GCM key that lives in Android Keystore. Keeping one blob avoids
 * exposing individual preference keys such as access-token or refresh-token names.
 */
class KeystoreAuthStorage internal constructor(
    private val prefs: SharedPreferences,
    private val cipher: AuthStateCipher,
) : AuthStorage {
    private val lock = Any()
    private var state: MutableMap<String, AuthStorageEntry> = readState()

    override fun getString(key: String): String? = synchronized(lock) {
        state[key]?.takeIf { it.type == AuthStorageValueType.STRING }?.value
    }

    override fun getLong(key: String): Long? = synchronized(lock) {
        state[key]
            ?.takeIf { it.type == AuthStorageValueType.LONG }
            ?.value
            ?.toLongOrNull()
    }

    override fun getBoolean(key: String): Boolean? = synchronized(lock) {
        state[key]
            ?.takeIf { it.type == AuthStorageValueType.BOOLEAN }
            ?.value
            ?.toBooleanStrictOrNull()
    }

    override fun edit(block: AuthStorage.Editor.() -> Unit) {
        synchronized(lock) {
            val next = state.toMutableMap()
            val editor = object : AuthStorage.Editor {
                override fun putString(key: String, value: String?) {
                    if (value == null) {
                        next.remove(key)
                    } else {
                        next[key] = AuthStorageEntry(key, AuthStorageValueType.STRING, value)
                    }
                }

                override fun putLong(key: String, value: Long) {
                    next[key] = AuthStorageEntry(key, AuthStorageValueType.LONG, value.toString())
                }

                override fun putBoolean(key: String, value: Boolean) {
                    next[key] = AuthStorageEntry(key, AuthStorageValueType.BOOLEAN, value.toString())
                }

                override fun remove(key: String) {
                    next.remove(key)
                }
            }
            editor.block()
            writeState(next)
            state = next
        }
    }

    override fun clearAll() {
        synchronized(lock) {
            prefs.edit().remove(BLOB_KEY).apply()
            state = mutableMapOf()
        }
    }

    internal fun migrateMissing(entries: List<AuthStorageEntry>) {
        if (entries.isEmpty()) return
        synchronized(lock) {
            val next = state.toMutableMap()
            entries.forEach { entry ->
                next.putIfAbsent(entry.key, entry)
            }
            writeState(next)
            state = next
        }
    }

    private fun readState(): MutableMap<String, AuthStorageEntry> {
        val encrypted = prefs.getString(BLOB_KEY, null) ?: return mutableMapOf()
        return runCatching {
            decodeState(cipher.decrypt(encrypted)).toMutableMap()
        }.onFailure { error ->
            Log.w(TAG, "auth_storage_read_failed", error)
        }.getOrDefault(mutableMapOf())
    }

    private fun writeState(next: Map<String, AuthStorageEntry>) {
        if (next.isEmpty()) {
            prefs.edit().remove(BLOB_KEY).apply()
            return
        }
        val encrypted = cipher.encrypt(encodeState(next))
        prefs.edit().putString(BLOB_KEY, encrypted).apply()
    }

    companion object {
        fun createMigrating(context: Context): KeystoreAuthStorage {
            val appContext = context.applicationContext
            val prefs = appContext.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
            val storage = KeystoreAuthStorage(
                prefs = prefs,
                cipher = AndroidKeystoreAuthStateCipher(),
            )
            storage.migrateLegacyIfNeeded(appContext)
            return storage
        }

        private fun KeystoreAuthStorage.migrateLegacyIfNeeded(context: Context) {
            if (prefs.getBoolean(LEGACY_MIGRATED_KEY, false)) return
            val migrated = runCatching {
                migrateMissing(LegacyEncryptedAuthStorage.readKnownValues(context))
                true
            }.onFailure { error ->
                Log.w(TAG, "auth_storage_legacy_migration_failed", error)
            }.getOrDefault(false)
            if (migrated) {
                prefs.edit().putBoolean(LEGACY_MIGRATED_KEY, true).apply()
            }
        }

        private const val TAG = "Igloo/AuthStorage"
        private const val PREFS_NAME = "igloo-auth-keystore"
        private const val BLOB_KEY = "auth_state"
        private const val LEGACY_MIGRATED_KEY = "legacy_encrypted_prefs_migrated"
    }
}

internal interface AuthStateCipher {
    fun encrypt(plaintext: String): String
    fun decrypt(payload: String): String
}

internal class AndroidKeystoreAuthStateCipher(
    private val keyAlias: String = KEY_ALIAS,
) : AuthStateCipher {
    override fun encrypt(plaintext: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey())
        val ciphertext = cipher.doFinal(plaintext.toByteArray(Charsets.UTF_8))
        return listOf(
            PAYLOAD_VERSION,
            base64Encode(cipher.iv),
            base64Encode(ciphertext),
        ).joinToString(":")
    }

    override fun decrypt(payload: String): String {
        val parts = payload.split(":")
        require(parts.size == 3 && parts[0] == PAYLOAD_VERSION) {
            "unsupported auth storage payload"
        }
        val iv = base64Decode(parts[1])
        val ciphertext = base64Decode(parts[2])
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, getOrCreateKey(), GCMParameterSpec(GCM_TAG_BITS, iv))
        return String(cipher.doFinal(ciphertext), Charsets.UTF_8)
    }

    private fun getOrCreateKey(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getKey(keyAlias, null) as? SecretKey)?.let { return it }
        synchronized(AndroidKeystoreAuthStateCipher::class.java) {
            val reloaded = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
            (reloaded.getKey(keyAlias, null) as? SecretKey)?.let { return it }
            val keyGenerator = KeyGenerator.getInstance(
                KeyProperties.KEY_ALGORITHM_AES,
                ANDROID_KEYSTORE,
            )
            val keySpec = KeyGenParameterSpec.Builder(
                keyAlias,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setKeySize(KEY_SIZE_BITS)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build()
            keyGenerator.init(keySpec)
            return keyGenerator.generateKey()
        }
    }

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "igloo-auth-storage-v1"
        const val KEY_SIZE_BITS = 256
        const val GCM_TAG_BITS = 128
        const val PAYLOAD_VERSION = "v1"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
    }
}

private fun encodeState(entries: Map<String, AuthStorageEntry>): String =
    entries.values.joinToString("\n") { entry ->
        listOf(
            entry.type.token,
            base64Encode(entry.key.toByteArray(Charsets.UTF_8)),
            base64Encode(entry.value.toByteArray(Charsets.UTF_8)),
        ).joinToString("\t")
    }

private fun decodeState(encoded: String): Map<String, AuthStorageEntry> {
    if (encoded.isBlank()) return emptyMap()
    return encoded.lineSequence()
        .filter { it.isNotBlank() }
        .mapNotNull { line ->
            val parts = line.split("\t")
            if (parts.size != 3) return@mapNotNull null
            val type = AuthStorageValueType.fromToken(parts[0]) ?: return@mapNotNull null
            val key = String(base64Decode(parts[1]), Charsets.UTF_8)
            val value = String(base64Decode(parts[2]), Charsets.UTF_8)
            AuthStorageEntry(key = key, type = type, value = value)
        }
        .associateBy { it.key }
}

private fun base64Encode(bytes: ByteArray): String =
    Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)

private fun base64Decode(value: String): ByteArray =
    Base64.getUrlDecoder().decode(value)

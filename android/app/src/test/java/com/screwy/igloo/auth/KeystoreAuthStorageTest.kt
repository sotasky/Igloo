package com.screwy.igloo.auth

import android.content.Context
import android.content.SharedPreferences
import androidx.test.core.app.ApplicationProvider
import java.util.Base64
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class KeystoreAuthStorageTest {
    private lateinit var prefs: SharedPreferences

    @Before
    fun setUp() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        prefs = context.getSharedPreferences("keystore-auth-storage-test", Context.MODE_PRIVATE)
        prefs.edit().clear().commit()
    }

    @Test
    fun persistsTypedValuesInEncryptedBlob() {
        val storage = newStorage()

        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, "acc-secret")
            putLong(AuthKeys.ACCESS_EXPIRES_AT_MS, 1234L)
            putBoolean(AuthKeys.IS_ADMIN, true)
        }
        val reloaded = newStorage()

        assertEquals("acc-secret", reloaded.getString(AuthKeys.ACCESS_TOKEN))
        assertEquals(1234L, reloaded.getLong(AuthKeys.ACCESS_EXPIRES_AT_MS))
        assertTrue(reloaded.getBoolean(AuthKeys.IS_ADMIN) == true)
        assertFalse("plain token should not be in preference values", rawPrefs().contains("acc-secret"))
        assertFalse("auth key names should not be preference keys", prefs.all.containsKey(AuthKeys.ACCESS_TOKEN))
    }

    @Test
    fun editCanRemoveValues() {
        val storage = newStorage()
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, "acc-secret")
            putString(AuthKeys.REFRESH_TOKEN, "ref-secret")
        }

        storage.edit {
            remove(AuthKeys.ACCESS_TOKEN)
            putString(AuthKeys.REFRESH_TOKEN, null)
        }

        assertNull(storage.getString(AuthKeys.ACCESS_TOKEN))
        assertNull(storage.getString(AuthKeys.REFRESH_TOKEN))
    }

    @Test
    fun migrationCopiesOnlyMissingValues() {
        val storage = newStorage()
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, "new-access")
        }

        storage.migrateMissing(
            listOf(
                AuthStorageEntry(
                    AuthKeys.ACCESS_TOKEN,
                    AuthStorageValueType.STRING,
                    "legacy-access",
                ),
                AuthStorageEntry(
                    AuthKeys.REFRESH_TOKEN,
                    AuthStorageValueType.STRING,
                    "legacy-refresh",
                ),
                AuthStorageEntry(
                    AuthKeys.ACCESS_EXPIRES_AT_MS,
                    AuthStorageValueType.LONG,
                    "999",
                ),
            ),
        )

        val reloaded = newStorage()
        assertEquals("new-access", reloaded.getString(AuthKeys.ACCESS_TOKEN))
        assertEquals("legacy-refresh", reloaded.getString(AuthKeys.REFRESH_TOKEN))
        assertEquals(999L, reloaded.getLong(AuthKeys.ACCESS_EXPIRES_AT_MS))
    }

    @Test
    fun clearAllRemovesAuthState() {
        val storage = newStorage()
        storage.edit {
            putString(AuthKeys.ACCESS_TOKEN, "acc-secret")
            putBoolean(AuthKeys.IS_ADMIN, true)
        }

        storage.clearAll()

        assertNull(storage.getString(AuthKeys.ACCESS_TOKEN))
        assertNull(newStorage().getBoolean(AuthKeys.IS_ADMIN))
    }

    private fun newStorage(): KeystoreAuthStorage =
        KeystoreAuthStorage(prefs = prefs, cipher = TestAuthStateCipher)

    private fun rawPrefs(): String =
        prefs.all.entries.joinToString("\n") { "${it.key}=${it.value}" }

    private object TestAuthStateCipher : AuthStateCipher {
        override fun encrypt(plaintext: String): String =
            "test:" + Base64.getEncoder().encodeToString(plaintext.toByteArray(Charsets.UTF_8))

        override fun decrypt(payload: String): String {
            require(payload.startsWith("test:"))
            return String(Base64.getDecoder().decode(payload.removePrefix("test:")), Charsets.UTF_8)
        }
    }
}

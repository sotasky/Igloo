package com.screwy.igloo.i18n

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class AppLanguageStoreTest {

    private lateinit var store: AppLanguageStore

    @Before fun setUp() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        store = AppLanguageStore(context)
        store.setLanguageTag("")
    }

    @After fun tearDown() {
        store.setLanguageTag("")
    }

    @Test fun blankLanguageMeansSystemDefault() {
        store.setLanguageTag("")

        assertEquals(AppLanguageStore.SYSTEM_LANGUAGE, store.languageTagSync())
    }

    @Test fun languageTagNormalizesAndPersists() {
        store.setLanguageTag("tr_TR")

        assertEquals("tr-TR", store.languageTagSync())

        val reloaded = AppLanguageStore(ApplicationProvider.getApplicationContext<Context>())
        assertEquals("tr-TR", reloaded.languageTagSync())
    }

    @Test fun invalidLanguageFallsBackToSystem() {
        store.setLanguageTag("not a locale")

        assertEquals(AppLanguageStore.SYSTEM_LANGUAGE, store.languageTagSync())
    }
}

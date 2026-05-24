package com.screwy.igloo.media

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class ForegroundPromoterTest {

    private lateinit var context: Context
    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var logger: Logger

    @Before fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        db = RoomTestSupport.freshDb()
        val prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 1_000L })
        logger = Logger(prefs = prefs, sink = InMemoryLogSink(), scope = scope, nowMsProvider = { 1_000L })
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test fun externalForegroundLeaseSuppressesMediaServiceStart() {
        val starts = AtomicInteger(0)
        val stops = AtomicInteger(0)
        val promoter = ForegroundPromoter(
            context = context,
            logger = logger,
            startForegroundService = { starts.incrementAndGet() },
            stopForegroundService = { stops.incrementAndGet() },
        )

        val lease = promoter.acquireExternalForegroundLease()
        promoter.startActiveDrain()
        promoter.finishActiveDrain()
        lease.close()

        assertEquals(0, starts.get())
        assertEquals(0, stops.get())
    }

    @Test fun mediaServiceStartsOnceAndStopsWhenInflightSetEmpties() {
        val starts = AtomicInteger(0)
        val stops = AtomicInteger(0)
        val promoter = ForegroundPromoter(
            context = context,
            logger = logger,
            startForegroundService = { starts.incrementAndGet() },
            stopForegroundService = { stops.incrementAndGet() },
        )

        promoter.startDownloading(listOf("asset-a"))
        promoter.startDownloading(listOf("asset-b"))
        promoter.finishedBatch(listOf("asset-a"))
        promoter.finishedBatch(listOf("asset-b"))

        assertEquals(1, starts.get())
        assertEquals(1, stops.get())
    }
}

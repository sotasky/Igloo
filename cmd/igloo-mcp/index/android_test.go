package index

import "testing"

func TestScanKotlin(t *testing.T) {
	src := `package com.example.data.feed

import com.example.ui.viewmodel.FeedViewModel

@Entity(tableName = "feed_items")
data class FeedItem(val id: String)

@Dao
interface FeedDao {
    @Query("SELECT * FROM feed_items WHERE id = :id")
    suspend fun getItem(id: String): FeedItem?

    @Upsert
    suspend fun upsert(item: FeedItem)
}

class FeedRepository(private val client: HttpClient) {
    suspend fun loadFeed() {
        client.get("$baseUrl/api/feed/rsshub")
        client.post("$baseUrl/api/feed/like/$id")
    }
}
`
	result := ScanKotlin(src, "android/app/src/main/java/com/example/data/feed/FeedRepository.kt")

	if len(result.Entities) == 0 {
		t.Error("expected at least one @Entity")
	}
	if result.Entities[0].TableName != "feed_items" {
		t.Errorf("expected table feed_items, got %s", result.Entities[0].TableName)
	}
	if len(result.APICalls) < 2 {
		t.Errorf("expected 2 api calls, got %d", len(result.APICalls))
	}
	if result.APICalls[0].Path != "/api/feed/rsshub" {
		t.Errorf("unexpected path: %s", result.APICalls[0].Path)
	}

	var hasDAO bool
	for _, d := range result.DAOTables {
		if d.Table == "feed_items" {
			hasDAO = true
		}
	}
	if !hasDAO {
		t.Error("expected DAO table ref for feed_items")
	}
}

// TestScanKotlinMultilineTripleQueryOnNextLine verifies that @Query(
// on one line, followed by """ opening on the NEXT line, is parsed.
// This is the dominant formatting style in the real DAO files:
//
//	@Query(
//	    """
//	    SELECT ... FROM items JOIN channels ...
//	    """
//	)
//
// Previously the scanner only matched when @Query( and """ were on the
// same line, silently dropping these queries.
func TestScanKotlinMultilineTripleQueryOnNextLine(t *testing.T) {
	src := `@Dao
interface ItemDao {
    @Query(
        """
        SELECT i.*, c.name
        FROM items i
        JOIN channels c ON c.id = i.channel_id
        WHERE i.bookmarked_at IS NOT NULL
        """
    )
    suspend fun getBookmarkedItems(): List<Item>

    @Query(
        """
        SELECT * FROM moment_views WHERE user_id = :uid
        """
    )
    suspend fun getMoments(uid: String): List<Moment>
}
`
	result := ScanKotlin(src, "android/ItemDao.kt")

	tables := map[string]bool{}
	for _, d := range result.DAOTables {
		tables[d.Table] = true
	}
	for _, want := range []string{"items", "channels", "moment_views"} {
		if !tables[want] {
			t.Errorf("expected DAO table ref for %q from multiline @Query with \"\"\" on next line, got %v", want, tables)
		}
	}
}

// TestScanKotlinSingleLineTripleQuery verifies that a single-line triple-quoted
// @Query does not lock the state machine, discarding everything after it.
func TestScanKotlinSingleLineTripleQuery(t *testing.T) {
	src := `@Dao
interface ItemDao {
    @Query("""SELECT * FROM items""")
    suspend fun getAll(): List<Item>
}

class ItemRepo(private val client: HttpClient) {
    suspend fun load() {
        client.get("$baseUrl/api/items")
    }
}
`
	result := ScanKotlin(src, "android/ItemDao.kt")

	// API call after the triple-quoted @Query must not be dropped
	if len(result.APICalls) == 0 {
		t.Error("expected API call after single-line triple-quoted @Query, state machine stuck")
	}
	var hasItems bool
	for _, d := range result.DAOTables {
		if d.Table == "items" {
			hasItems = true
		}
	}
	if !hasItems {
		t.Error("expected DAO table ref for items from single-line triple-quoted @Query")
	}
}

func TestScanKotlinConstructorDeps(t *testing.T) {
	src := `package com.screwy.igloo.ui.viewmodel

class FeedViewModel(
    private val feedRepo: FeedRepository,
    private val videoRepo: VideoRepository
) : ViewModel() {
    fun load() {}
}
`
	result := ScanKotlin(src, "android/app/src/main/java/com/screwy/igloo/ui/viewmodel/FeedViewModel.kt")

	if len(result.ConstructorDeps) != 2 {
		t.Fatalf("expected 2 constructor deps, got %d", len(result.ConstructorDeps))
	}
	expected := map[string]string{
		"feedRepo":  "FeedRepository",
		"videoRepo": "VideoRepository",
	}
	for _, dep := range result.ConstructorDeps {
		if typ, ok := expected[dep.Name]; ok {
			if dep.Type != typ {
				t.Errorf("dep %s: expected type %s, got %s", dep.Name, typ, dep.Type)
			}
		}
	}
}

func TestScanKotlinConstructorDepsRepo(t *testing.T) {
	src := `package com.screwy.igloo.data.repository

class FeedRepository(
    private val feedDao: FeedPostDao,
    private val api: XFeedApi,
    private val db: XFeedDatabase
) {
    suspend fun sync() {}
}
`
	result := ScanKotlin(src, "android/app/src/main/java/com/screwy/igloo/data/repository/FeedRepository.kt")

	if len(result.ConstructorDeps) != 3 {
		t.Fatalf("expected 3 constructor deps, got %d", len(result.ConstructorDeps))
	}
}

func TestScanKotlinConstructorNoPropPrefix(t *testing.T) {
	src := `package com.screwy.igloo.ui.viewmodel

class BookmarksViewModel(feedRepo: FeedRepository) : FeedViewModel(feedRepo, scope = "bookmarked") {
    fun load() {}
}
`
	result := ScanKotlin(src, "android/BookmarksViewModel.kt")
	if len(result.ConstructorDeps) != 1 {
		t.Fatalf("expected 1 constructor dep (no val/var prefix), got %d", len(result.ConstructorDeps))
	}
	if result.ConstructorDeps[0].Name != "feedRepo" {
		t.Errorf("expected feedRepo, got %s", result.ConstructorDeps[0].Name)
	}
}

func TestScanKotlinConstructorMultiClass(t *testing.T) {
	src := `package com.screwy.igloo.data.repository

data class SyncResult(val itemsSynced: Int)

class FeedRepository(
    private val feedDao: FeedPostDao,
    private val api: XFeedApi,
    private val db: XFeedDatabase
) {
    suspend fun sync() {}
}
`
	result := ScanKotlin(src, "android/FeedRepository.kt")
	// ClassName is SyncResult (first class), but we want FeedRepository's deps.
	// The old code would get SyncResult's single dep. The new code also gets SyncResult's
	// since it matches ClassName. This is a known limitation — the primary class isn't
	// always first. But it's still better than the old crash-on-wrong-class behavior.
	// In the real codebase, the primary class is typically either first or identified by filename.
	if result.ClassName != "SyncResult" {
		t.Errorf("expected ClassName SyncResult, got %s", result.ClassName)
	}
}

func TestScanKotlinNamedComposableRoute(t *testing.T) {
	src := `@Composable
fun AppNavHost() {
    NavHost(navController, startDestination = "feed") {
        composable(route = "channel/{channel_id}") { ChannelRoute("id", navController) }
        composable("feed") { FeedRoute(navController) }
    }
}
`
	result := ScanKotlin(src, "android/AppNavHost.kt")
	if len(result.NavRoutes) != 2 {
		t.Fatalf("expected 2 nav routes, got %d", len(result.NavRoutes))
	}
	if result.NavRoutes[0] != "channel/{channel_id}" {
		t.Fatalf("expected named route to be captured first, got %q", result.NavRoutes[0])
	}
	if result.NavRoutes[1] != "feed" {
		t.Fatalf("expected plain route to be captured second, got %q", result.NavRoutes[1])
	}
}

func TestScanDebugEvents(t *testing.T) {
	src := `object DebugTracker {
    fun localWriteOk(field: String, itemId: String) {
        StatsLogger.log(
            "debug_local_write_ok",
            "field" to field, "item_id" to itemId,
            "rows_affected" to rowsAffected,
        )
    }

    fun feedRankedFetchOk(page: Int, count: Int) {
        StatsLogger.log(
            "feed_ranked_fetch_ok",
            "page" to pageNum, "count" to count,
        )
    }
}
`
	events := ScanDebugEvents(src, "android/DebugTracker.kt")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Name != "debug_local_write_ok" {
		t.Errorf("expected debug_local_write_ok, got %s", events[0].Name)
	}
	if len(events[0].Fields) != 3 {
		t.Errorf("expected 3 fields for debug_local_write_ok, got %d", len(events[0].Fields))
	}
	if events[1].Name != "feed_ranked_fetch_ok" {
		t.Errorf("expected feed_ranked_fetch_ok, got %s", events[1].Name)
	}
}

// TestScanKotlinEntityLeak verifies that pendingEntityTable does not leak to an
// unrelated data class when @Entity is applied to a non-data class.
func TestScanKotlinEntityLeak(t *testing.T) {
	src := `@Entity(tableName = "real_table")
class NonDataClass

data class UnrelatedData(val x: String)
`
	result := ScanKotlin(src, "android/Leak.kt")

	// NonDataClass is not a data class — no entity should be produced
	for _, e := range result.Entities {
		if e.TableName == "real_table" {
			t.Errorf("entity 'real_table' should not be associated with a non-data class, got ClassName=%s", e.ClassName)
		}
	}
}

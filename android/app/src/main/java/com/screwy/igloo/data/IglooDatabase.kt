package com.screwy.igloo.data

import android.content.Context
import android.util.Log
import androidx.room.Database
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.sqlite.db.SupportSQLiteDatabase
import java.io.File
import com.screwy.igloo.data.dao.BookmarkCategoryDao
import com.screwy.igloo.data.dao.BookmarkDao
import com.screwy.igloo.data.dao.BookmarkLabelDao
import com.screwy.igloo.data.dao.BookmarkReadDao
import com.screwy.igloo.data.dao.ChannelDao
import com.screwy.igloo.data.dao.ChannelFollowDao
import com.screwy.igloo.data.dao.ChannelProfileDao
import com.screwy.igloo.data.dao.ChannelReadDao
import com.screwy.igloo.data.dao.ChannelSettingDao
import com.screwy.igloo.data.dao.ChannelStarDao
import com.screwy.igloo.data.dao.CursorDao
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.FeedItemDao
import com.screwy.igloo.data.dao.FeedLikeDao
import com.screwy.igloo.data.dao.FeedRankDao
import com.screwy.igloo.data.dao.FeedReadDao
import com.screwy.igloo.data.dao.FeedSeenDao
import com.screwy.igloo.data.dao.FeedThreadContextDao
import com.screwy.igloo.data.dao.MediaInventoryDao
import com.screwy.igloo.data.dao.MomentReadDao
import com.screwy.igloo.data.dao.MomentViewDao
import com.screwy.igloo.data.dao.MutedAccountDao
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.data.dao.PreferenceDao
import com.screwy.igloo.data.dao.RetweetSourceDao
import com.screwy.igloo.data.dao.SponsorBlockCheckedDao
import com.screwy.igloo.data.dao.SponsorBlockSegmentDao
import com.screwy.igloo.data.dao.VideoCommentDao
import com.screwy.igloo.data.dao.VideoDao
import com.screwy.igloo.data.dao.VideoRepostSourceDao
import com.screwy.igloo.data.dao.VideoReadDao
import com.screwy.igloo.data.dao.WatchHistoryDao
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.BookmarkLabelEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.CursorEntity
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncGenerationEntity
import com.screwy.igloo.data.entity.AndroidSyncItemEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.FeedThreadContextEntity
import com.screwy.igloo.data.entity.FeedTimelineEntryEntity
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MutedAccountEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.data.entity.PreferenceEntity
import com.screwy.igloo.data.entity.RetweetSourceEntity
import com.screwy.igloo.data.entity.SponsorBlockCheckedEntity
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.VideoRepostSourceEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity

/**
 * The Igloo local database.
 *
 * Per-user scope: the database file lives at `<appFilesDir>/databases/igloo-<username>.db`
 * and is opened/closed/deleted via `DatabaseHolder`. UI layers resolve DAOs through the
 * holder rather than holding a `IglooDatabase` reference directly — logout swaps the
 * current instance transparently.
 *
 * ### Migrations
 *
 * Every schema bump MUST add an explicit `Migration` in `IglooMigrations`. Committed
 * Room schema JSON intentionally starts at `SUPPORTED_SCHEMA_BASELINE_VERSION`; older
 * migration objects may stay in code for installed databases, but snapshots before that
 * baseline are not required evidence. Destructive fallback exists only as a backstop for
 * upgrade paths that predate the migration ladder (any version pair without a registered
 * migration will still drop all tables). It is NOT a license to skip writing a real
 * migration: dropping the cache leaves orphan media files on disk (the inventory rows are
 * wiped, but the underlying files aren't), so the app reports 0 MB cached while still
 * using GBs of storage and re-sync may not redownload everything that was lost. Add an
 * `ALTER TABLE` migration for any column add — Room's SQLite layer handles trivial column
 * adds without rewriting the table.
 */
@Database(
    entities = [
        // Server-mirrored core
        FeedItemEntity::class,
        FeedThreadContextEntity::class,
        VideoEntity::class,
        ChannelEntity::class,
        ChannelProfileEntity::class,
        VideoCommentEntity::class,
        RetweetSourceEntity::class,
        VideoRepostSourceEntity::class,
        SponsorBlockSegmentEntity::class,
        SponsorBlockCheckedEntity::class,
        // Server-mirrored user-state side tables
        FeedLikeEntity::class,
        FeedRankEntity::class,
        BookmarkEntity::class,
        BookmarkCategoryEntity::class,
        BookmarkLabelEntity::class,
        FeedSeenEntity::class,
        MomentViewEntity::class,
        WatchHistoryEntity::class,
        MutedAccountEntity::class,
        ChannelFollowEntity::class,
        ChannelStarEntity::class,
        // Android-only
        FeedTimelineEntryEntity::class,
        OutboxEntity::class,
        PreferenceEntity::class,
        CursorEntity::class,
        ChannelSettingEntity::class,
        MediaInventoryEntity::class,
        AndroidSyncGenerationEntity::class,
        AndroidSyncItemEntity::class,
        AndroidSyncAssetEntity::class,
    ],
    version = IglooMigrations.CURRENT_SCHEMA_VERSION,
    exportSchema = true,
)
abstract class IglooDatabase : RoomDatabase() {

    // Per-entity DAOs
    abstract fun feedItemDao():             FeedItemDao
    abstract fun videoDao():                VideoDao
    abstract fun channelDao():              ChannelDao
    abstract fun channelProfileDao():       ChannelProfileDao
    abstract fun videoCommentDao():         VideoCommentDao
    abstract fun retweetSourceDao():        RetweetSourceDao
    abstract fun videoRepostSourceDao():    VideoRepostSourceDao
    abstract fun sponsorBlockSegmentDao():  SponsorBlockSegmentDao
    abstract fun sponsorBlockCheckedDao():  SponsorBlockCheckedDao
    abstract fun feedLikeDao():             FeedLikeDao
    abstract fun feedRankDao():             FeedRankDao
    abstract fun feedThreadContextDao():    FeedThreadContextDao
    abstract fun bookmarkDao():             BookmarkDao
    abstract fun bookmarkCategoryDao():     BookmarkCategoryDao
    abstract fun bookmarkLabelDao():        BookmarkLabelDao
    abstract fun feedSeenDao():             FeedSeenDao
    abstract fun momentViewDao():           MomentViewDao
    abstract fun watchHistoryDao():         WatchHistoryDao
    abstract fun mutedAccountDao():         MutedAccountDao
    abstract fun channelFollowDao():        ChannelFollowDao
    abstract fun channelStarDao():          ChannelStarDao
    abstract fun channelSettingDao():       ChannelSettingDao
    abstract fun outboxDao():               OutboxDao
    abstract fun preferenceDao():           PreferenceDao
    abstract fun cursorDao():               CursorDao
    abstract fun mediaInventoryDao():       MediaInventoryDao
    abstract fun androidSyncDao():         AndroidSyncDao

    // Composite read DAOs
    abstract fun feedReadDao():       FeedReadDao
    abstract fun momentReadDao():     MomentReadDao
    abstract fun videoReadDao():      VideoReadDao
    abstract fun bookmarkReadDao():   BookmarkReadDao
    abstract fun channelReadDao():    ChannelReadDao

    companion object {
        const val DB_FILE_PREFIX = "igloo-"
        const val DB_FILE_SUFFIX = ".db"

        /** File name for a user's DB, sanitized against filesystem weirdness. */
        fun fileNameFor(username: String): String =
            "$DB_FILE_PREFIX${sanitizeUsername(username)}$DB_FILE_SUFFIX"

        /**
         * Username → filesystem-safe slug. Keeps alphanumerics, `.`, `_`, `-`; replaces
         * everything else with `_`. Lowercased so case-only collisions don't produce
         * distinct DB files. Empty input becomes `anonymous` so the path still resolves.
         */
        fun sanitizeUsername(username: String): String {
            val slug = buildString(username.length) {
                for (c in username) {
                    append(
                        if (c.isLetterOrDigit() || c == '.' || c == '_' || c == '-') c.lowercaseChar()
                        else '_',
                    )
                }
            }
            return slug.ifEmpty { "anonymous" }
        }

        fun build(context: Context, fileName: String): IglooDatabase {
            val appCtx = context.applicationContext
            // Tied to the iglooMediaModule "mediaRoot" binding — must stay in sync.
            val mediaRoot = File(appCtx.filesDir, "media")
            return Room.databaseBuilder(appCtx, IglooDatabase::class.java, fileName)
                .addMigrations(*IglooMigrations.ALL)
                .fallbackToDestructiveMigration(dropAllTables = true)
                .addCallback(object : RoomDatabase.Callback() {
                    override fun onDestructiveMigration(db: SupportSQLiteDatabase) {
                        // Local media rows just got dropped; cached files may now
                        // be orphans. Wipe them too so storage and DB state stay
                        // consistent. Sync re-materializes media on demand.
                        if (mediaRoot.exists()) {
                            val ok = mediaRoot.deleteRecursively()
                            mediaRoot.mkdirs()
                            Log.i("IglooDatabase", "destructive migration: media cache wiped (success=$ok)")
                        }
                    }
                })
                .build()
        }

        fun buildForUser(context: Context, username: String): IglooDatabase =
            build(context, fileNameFor(username))
    }
}

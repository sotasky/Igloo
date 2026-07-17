package com.screwy.igloo.data

import android.content.Context
import androidx.room.Database
import androidx.room.Room
import androidx.room.RoomDatabase
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.BookmarkCategoryDao
import com.screwy.igloo.data.dao.BookmarkDao
import com.screwy.igloo.data.dao.BookmarkReadDao
import com.screwy.igloo.data.dao.ChannelDao
import com.screwy.igloo.data.dao.ChannelFollowDao
import com.screwy.igloo.data.dao.ChannelProfileDao
import com.screwy.igloo.data.dao.ChannelReadDao
import com.screwy.igloo.data.dao.ChannelSettingDao
import com.screwy.igloo.data.dao.ChannelStarDao
import com.screwy.igloo.data.dao.FeedItemDao
import com.screwy.igloo.data.dao.FeedLikeDao
import com.screwy.igloo.data.dao.FeedRankDao
import com.screwy.igloo.data.dao.FeedReadDao
import com.screwy.igloo.data.dao.FeedSeenDao
import com.screwy.igloo.data.dao.MomentReadDao
import com.screwy.igloo.data.dao.MomentViewDao
import com.screwy.igloo.data.dao.MomentsCursorDao
import com.screwy.igloo.data.dao.MutedChannelDao
import com.screwy.igloo.data.dao.OfflineVideoDownloadDao
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.data.dao.PreferenceDao
import com.screwy.igloo.data.dao.RetweetSourceDao
import com.screwy.igloo.data.dao.SponsorBlockCheckedDao
import com.screwy.igloo.data.dao.SponsorBlockSegmentDao
import com.screwy.igloo.data.dao.VideoCommentDao
import com.screwy.igloo.data.dao.VideoDao
import com.screwy.igloo.data.dao.VideoReadDao
import com.screwy.igloo.data.dao.VideoRepostSourceDao
import com.screwy.igloo.data.dao.WatchHistoryDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncHeadEntity
import com.screwy.igloo.data.entity.AndroidSyncStateEntity
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MomentsCursorEntity
import com.screwy.igloo.data.entity.MutedChannelEntity
import com.screwy.igloo.data.entity.OfflineVideoDownloadEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.data.entity.PreferenceEntity
import com.screwy.igloo.data.entity.RetweetSourceEntity
import com.screwy.igloo.data.entity.SponsorBlockCheckedEntity
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.VideoRepostSourceEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity

@Database(
    entities =
        [
            // Server-mirrored core
            FeedItemEntity::class,
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
            FeedSeenEntity::class,
            MomentViewEntity::class,
            MomentsCursorEntity::class,
            WatchHistoryEntity::class,
            MutedChannelEntity::class,
            ChannelFollowEntity::class,
            ChannelStarEntity::class,
            // Android-only
            OutboxEntity::class,
            PreferenceEntity::class,
            ChannelSettingEntity::class,
            AndroidSyncStateEntity::class,
            AndroidSyncHeadEntity::class,
            AndroidSyncAssetEntity::class,
            OfflineVideoDownloadEntity::class,
        ],
    version = 42,
    exportSchema = true,
)
abstract class IglooDatabase : RoomDatabase() {

    // Per-entity DAOs
    abstract fun feedItemDao(): FeedItemDao

    abstract fun videoDao(): VideoDao

    abstract fun channelDao(): ChannelDao

    abstract fun channelProfileDao(): ChannelProfileDao

    abstract fun videoCommentDao(): VideoCommentDao

    abstract fun retweetSourceDao(): RetweetSourceDao

    abstract fun videoRepostSourceDao(): VideoRepostSourceDao

    abstract fun sponsorBlockSegmentDao(): SponsorBlockSegmentDao

    abstract fun sponsorBlockCheckedDao(): SponsorBlockCheckedDao

    abstract fun feedLikeDao(): FeedLikeDao

    abstract fun feedRankDao(): FeedRankDao

    abstract fun bookmarkDao(): BookmarkDao

    abstract fun bookmarkCategoryDao(): BookmarkCategoryDao

    abstract fun feedSeenDao(): FeedSeenDao

    abstract fun momentViewDao(): MomentViewDao

    abstract fun momentsCursorDao(): MomentsCursorDao

    abstract fun watchHistoryDao(): WatchHistoryDao

    abstract fun mutedChannelDao(): MutedChannelDao

    abstract fun channelFollowDao(): ChannelFollowDao

    abstract fun channelStarDao(): ChannelStarDao

    abstract fun channelSettingDao(): ChannelSettingDao

    abstract fun outboxDao(): OutboxDao

    abstract fun preferenceDao(): PreferenceDao

    abstract fun androidSyncDao(): AndroidSyncDao

    abstract fun offlineVideoDownloadDao(): OfflineVideoDownloadDao

    // Composite read DAOs
    abstract fun feedReadDao(): FeedReadDao

    abstract fun momentReadDao(): MomentReadDao

    abstract fun videoReadDao(): VideoReadDao

    abstract fun bookmarkReadDao(): BookmarkReadDao

    abstract fun channelReadDao(): ChannelReadDao

    companion object {
        const val DB_FILE_NAME = "igloo.db"

        fun build(context: Context): IglooDatabase {
            val appCtx = context.applicationContext
            return Room.databaseBuilder(appCtx, IglooDatabase::class.java, DB_FILE_NAME)
                .addMigrations(
                    IglooMigrations.MIGRATION_40_41,
                    IglooMigrations.MIGRATION_41_42,
                )
                .build()
        }
    }
}

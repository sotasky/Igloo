// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.

package com.screwy.igloo.ui.component

import android.view.ViewGroup
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import coil3.ImageLoader
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.SocialPostModel
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import kotlinx.coroutines.CoroutineScope

internal class NativeFeedAdapter(
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private val scope: CoroutineScope,
    private val getColors: () -> NativeFeedColors,
    private val getCallbacks: () -> NativeFeedCallbacks,
    private val inlineVideoManager: NativeInlineVideoManager,
    private val onItemsChanged: (List<NativeFeedAdapterItem>) -> Unit,
) : ListAdapter<NativeFeedAdapterItem, RecyclerView.ViewHolder>(Diff) {

    init {
        setHasStableIds(true)
    }

    override fun getItemViewType(position: Int): Int =
        when (getItem(position)) {
            is NativeFeedAdapterItem.Header -> ViewTypeHeader
            is NativeFeedAdapterItem.Post -> ViewTypePost
        }

    override fun getItemId(position: Int): Long = stableItemId(getItem(position).id)

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): RecyclerView.ViewHolder =
        when (viewType) {
            ViewTypeHeader ->
                NativeFeedChannelHeaderViewHolder(
                    context = parent.context,
                    imageLoader = imageLoader,
                    authTokens = authTokens,
                    iglooHostProvider = iglooHostProvider,
                    mediaResolvers = mediaResolvers,
                    scope = scope,
                    getColors = getColors,
                    getCallbacks = getCallbacks,
                )
            else ->
                NativeFeedViewHolder(
                    context = parent.context,
                    imageLoader = imageLoader,
                    authTokens = authTokens,
                    iglooHostProvider = iglooHostProvider,
                    mediaResolvers = mediaResolvers,
                    scope = scope,
                    getColors = getColors,
                    getCallbacks = getCallbacks,
                    inlineVideoManager = inlineVideoManager,
                )
        }

    override fun onBindViewHolder(holder: RecyclerView.ViewHolder, position: Int) {
        val viewType = getItemViewType(position)
        when (val item = getItem(position)) {
            is NativeFeedAdapterItem.Header ->
                (holder as NativeFeedChannelHeaderViewHolder).bind(item.header)
            is NativeFeedAdapterItem.Post -> (holder as NativeFeedViewHolder).bind(item)
        }
    }

    override fun onBindViewHolder(
        holder: RecyclerView.ViewHolder,
        position: Int,
        payloads: MutableList<Any>,
    ) {
        if (holder is NativeFeedViewHolder) {
            val actionPayload = payloads.singleOrNull() as? LikeBookmarkPayload
            val mediaPayload = payloads.singleOrNull() as? MediaPayload
            when {
                actionPayload != null -> {
                    holder.bindLikeBookmarkState(actionPayload.item)
                    return
                }
                mediaPayload != null -> {
                    holder.bindMediaState(mediaPayload.item)
                    return
                }
            }
        }
        super.onBindViewHolder(holder, position, payloads)
    }

    override fun onViewRecycled(holder: RecyclerView.ViewHolder) {
        when (holder) {
            is NativeFeedViewHolder -> holder.recycle()
            is NativeFeedChannelHeaderViewHolder -> holder.recycle()
        }
        super.onViewRecycled(holder)
    }

    override fun onCurrentListChanged(
        previousList: List<NativeFeedAdapterItem>,
        currentList: List<NativeFeedAdapterItem>,
    ) {
        super.onCurrentListChanged(previousList, currentList)
        onItemsChanged(currentList)
    }

    companion object {
        const val ViewTypeHeader = 0
        const val ViewTypePost = 1

        private val Diff =
            object : DiffUtil.ItemCallback<NativeFeedAdapterItem>() {
                override fun areItemsTheSame(
                    oldItem: NativeFeedAdapterItem,
                    newItem: NativeFeedAdapterItem,
                ): Boolean = oldItem.id == newItem.id

                override fun areContentsTheSame(
                    oldItem: NativeFeedAdapterItem,
                    newItem: NativeFeedAdapterItem,
                ): Boolean = oldItem == newItem

                override fun getChangePayload(
                    oldItem: NativeFeedAdapterItem,
                    newItem: NativeFeedAdapterItem,
                ): Any? =
                    when {
                        nativeFeedLikeBookmarkOnlyChange(oldItem, newItem) ->
                            LikeBookmarkPayload(newItem as NativeFeedAdapterItem.Post)
                        nativeFeedMediaOnlyChange(oldItem, newItem) ->
                            MediaPayload(newItem as NativeFeedAdapterItem.Post)
                        else -> null
                    }
            }
    }
}

private data class LikeBookmarkPayload(val item: NativeFeedAdapterItem.Post)

private data class MediaPayload(val item: NativeFeedAdapterItem.Post)

internal fun nativeFeedLikeBookmarkOnlyChange(
    oldItem: NativeFeedAdapterItem,
    newItem: NativeFeedAdapterItem,
): Boolean {
    if (oldItem !is NativeFeedAdapterItem.Post || newItem !is NativeFeedAdapterItem.Post) {
        return false
    }
    if (oldItem == newItem) return false
    return oldItem.withoutLikeBookmarkState() == newItem.withoutLikeBookmarkState()
}

internal fun nativeFeedMediaOnlyChange(
    oldItem: NativeFeedAdapterItem,
    newItem: NativeFeedAdapterItem,
): Boolean {
    if (oldItem !is NativeFeedAdapterItem.Post || newItem !is NativeFeedAdapterItem.Post) {
        return false
    }
    if (oldItem == newItem || oldItem.chainPosts.isNotEmpty() || newItem.chainPosts.isNotEmpty()) {
        return false
    }
    return oldItem.withoutMediaModels() == newItem.withoutMediaModels()
}

private fun NativeFeedAdapterItem.Post.withoutLikeBookmarkState(): NativeFeedAdapterItem.Post =
    copy(
        threaded = threaded.withoutLikeBookmarkState(),
        post = post.withoutLikeBookmarkState(),
        chainPosts = chainPosts.map(SocialPostModel::withoutLikeBookmarkState),
    )

private fun ThreadedFeedRow.withoutLikeBookmarkState(): ThreadedFeedRow =
    copy(
        row = row.withoutLikeBookmarkState(),
        chain = chain.map(FeedRow::withoutLikeBookmarkState),
    )

private fun SocialPostModel.withoutLikeBookmarkState(): SocialPostModel =
    copy(
        row = row.withoutLikeBookmarkState(),
        actions = actions.copy(isLiked = false, isBookmarked = false),
    )

private fun NativeFeedAdapterItem.Post.withoutMediaModels(): NativeFeedAdapterItem.Post =
    copy(
        post = post.withoutMediaModels(),
        chainPosts = chainPosts.map(SocialPostModel::withoutMediaModels),
    )

private fun SocialPostModel.withoutMediaModels(): SocialPostModel =
    copy(
        media = media.copy(grid = media.grid.withoutMediaModels()),
        quoteMedia = quoteMedia?.copy(grid = quoteMedia.grid.withoutMediaModels()),
    )

private fun FeedMediaGridModel.withoutMediaModels(): FeedMediaGridModel =
    copy(cells = emptyList(), inventoryLoaded = false)

private fun FeedRow.withoutLikeBookmarkState(): FeedRow =
    copy(
        isLiked = 0,
        likedAt = null,
        isBookmarked = 0,
        bookmarkCategoryId = null,
        bookmarkCustomTitle = null,
        bookmarkedAt = null,
        bookmarkAccountHandles = null,
        bookmarkMediaIndices = null,
    )

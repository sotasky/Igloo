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
        val actionPayload = payloads.lastOrNull() as? LikeBookmarkPayload
        if (actionPayload != null && holder is NativeFeedViewHolder) {
            holder.bindLikeBookmarkState(actionPayload.item)
            return
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

    fun postItems(): List<NativeFeedAdapterItem.Post> =
        currentList.filterIsInstance<NativeFeedAdapterItem.Post>()

    fun postIndexForAdapterIndex(adapterIndex: Int): Int? {
        if (adapterIndex !in currentList.indices) return null
        if (currentList[adapterIndex] !is NativeFeedAdapterItem.Post) return null
        return currentList.take(adapterIndex + 1).count { it is NativeFeedAdapterItem.Post } - 1
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
                    if (nativeFeedLikeBookmarkOnlyChange(oldItem, newItem)) {
                        LikeBookmarkPayload(newItem as NativeFeedAdapterItem.Post)
                    } else {
                        null
                    }
            }
    }
}

private data class LikeBookmarkPayload(val item: NativeFeedAdapterItem.Post)

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

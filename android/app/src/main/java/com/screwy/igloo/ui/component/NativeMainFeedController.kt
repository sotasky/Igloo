// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.

package com.screwy.igloo.ui.component

import android.content.Context
import android.view.ViewGroup
import androidx.compose.foundation.background
import androidx.compose.ui.unit.dp
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import coil3.ImageLoader
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.buildSocialPostModel
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.player.buildIglooPlayer
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

internal class NativeMainFeedController(
    private val context: Context,
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private var colors: NativeFeedColors,
    private var callbacks: NativeFeedCallbacks,
    seenBatcher: SeenBatcher,
    private val onScrollToTopVisibility: (Boolean) -> Unit,
    private val initialScrollAnchor: NativeFeedScrollAnchor,
    private val onScrollAnchorChanged: (NativeFeedScrollAnchor) -> Unit,
) {
    private val scopeJob = SupervisorJob()
    private val scope = CoroutineScope(scopeJob + Dispatchers.Main.immediate)
    private val layoutManager = LinearLayoutManager(context)
    private val seenTracker = PassedFeedRowsTracker(seenBatcher)
    private val inlineVideoManager =
        NativeInlineVideoManager(player = buildIglooPlayer(context, authTokens, iglooHostProvider))
    private var pendingInitialScrollAnchor: NativeFeedScrollAnchor? =
        initialScrollAnchor.takeIf { it.rowId != null }
    private var currentPosts: List<NativeFeedAdapterItem.Post> = emptyList()
    private var currentPostIds: List<String> = emptyList()
    private var postIndexByAdapterIndex = IntArray(0)
    private var currentMediaWindow: IntRange? = null
    private val adapter =
        NativeFeedAdapter(
            imageLoader = imageLoader,
            authTokens = authTokens,
            iglooHostProvider = iglooHostProvider,
            mediaResolvers = mediaResolvers,
            scope = scope,
            getColors = { colors },
            getCallbacks = { callbacks },
            inlineVideoManager = inlineVideoManager,
            onItemsChanged = ::cacheAdapterSnapshot,
        )
    val recyclerView: RecyclerView =
        RecyclerView(context).apply {
            setBackgroundColor(colors.background)
            this.layoutManager = this@NativeMainFeedController.layoutManager
            adapter = this@NativeMainFeedController.adapter
            itemAnimator = null
            setHasFixedSize(false)
            clipToPadding = false
            setPadding(0, dp(2), 0, dp(2))
            recycledViewPool.setMaxRecycledViews(NativeFeedAdapter.ViewTypePost, 12)
            addOnScrollListener(
                object : RecyclerView.OnScrollListener() {
                    override fun onScrolled(recyclerView: RecyclerView, dx: Int, dy: Int) {
                        onViewportChanged()
                    }

                    override fun onScrollStateChanged(recyclerView: RecyclerView, newState: Int) {
                        onViewportChanged()
                        if (newState == RecyclerView.SCROLL_STATE_IDLE) {
                            persistScrollAnchor()
                        }
                    }
                }
            )
        }
    val rootView: SwipeRefreshLayout =
        SwipeRefreshLayout(context).apply {
            setColorSchemeColors(colors.primary)
            setProgressBackgroundColorSchemeColor(colors.surfaceElevated)
            addView(
                recyclerView,
                ViewGroup.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT,
                ),
            )
            setOnRefreshListener { callbacks.onRefresh() }
        }

    fun update(
        rows: List<ThreadedFeedRow>,
        channelHeader: ChannelProfileHeaderUiModel?,
        mediaModels: Map<String, FeedMediaGridModel>,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
        isRefreshing: Boolean,
    ) {
        this.colors = colors
        this.callbacks = callbacks
        rootView.setColorSchemeColors(colors.primary)
        rootView.setProgressBackgroundColorSchemeColor(colors.surfaceElevated)
        rootView.isRefreshing = isRefreshing
        recyclerView.setBackgroundColor(
            if (channelHeader != null) colors.surface else colors.background
        )
        rootView.setBackgroundColor(
            if (channelHeader != null) colors.surface else colors.background
        )
        val items = buildList {
            channelHeader?.let { add(NativeFeedAdapterItem.Header(it)) }
            rows.forEach { threaded ->
                add(
                    NativeFeedAdapterItem.Post(
                        threaded = threaded,
                        post = buildSocialPostModel(threaded.row, mediaModels),
                        chainPosts =
                            threaded.chain.map { row -> buildSocialPostModel(row, mediaModels) },
                    )
                )
            }
        }

        adapter.submitList(items) {
            recyclerView.post {
                restoreInitialScrollAnchorIfNeeded()
                onViewportChanged()
            }
        }
    }

    fun scrollToTop() {
        recyclerView.stopScroll()
        layoutManager.scrollToPositionWithOffset(0, 0)
        onViewportChanged()
        persistScrollAnchor()
    }

    fun pauseVideo() {
        persistScrollAnchor()
        inlineVideoManager.pause()
    }

    fun release() {
        persistScrollAnchor()
        scopeJob.cancel()
        inlineVideoManager.release()
    }

    private fun onViewportChanged() {
        val firstVisible = layoutManager.findFirstVisibleItemPosition().coerceAtLeast(0)
        val firstVisiblePost = firstVisiblePostIndex(firstVisible).coerceAtLeast(0)
        seenTracker.onViewportChanged(
            rowIds = currentPostIds,
            firstVisibleIndex = firstVisiblePost,
        )
        onScrollToTopVisibility(firstVisiblePost > 5)
        updateNearVisibleMediaRows(firstVisiblePost)
        inlineVideoManager.selectFrom(recyclerView)
    }

    private fun restoreInitialScrollAnchorIfNeeded() {
        val anchor = pendingInitialScrollAnchor ?: return
        val adapterIndex = nativeFeedRestoreAdapterIndex(adapter.currentList, anchor) ?: return
        pendingInitialScrollAnchor = null
        recyclerView.stopScroll()
        layoutManager.scrollToPositionWithOffset(adapterIndex, anchor.offsetPx)
    }

    private fun cacheAdapterSnapshot(items: List<NativeFeedAdapterItem>) {
        val posts = ArrayList<NativeFeedAdapterItem.Post>(items.size)
        val postIds = ArrayList<String>(items.size)
        val indices = IntArray(items.size) { -1 }
        items.forEachIndexed { adapterIndex, item ->
            if (item is NativeFeedAdapterItem.Post) {
                indices[adapterIndex] = posts.size
                posts += item
                postIds += item.id
            }
        }
        currentPosts = posts
        currentPostIds = postIds
        postIndexByAdapterIndex = indices
        currentMediaWindow = null
    }

    private fun persistScrollAnchor() {
        onScrollAnchorChanged(nativeFeedScrollAnchor(adapter.currentList, layoutManager))
    }

    private fun firstVisiblePostIndex(firstVisibleAdapterIndex: Int): Int {
        return postIndexAt(firstVisibleAdapterIndex)
            ?: postIndexAt(firstVisibleAdapterIndex + 1)
            ?: 0
    }

    private fun updateNearVisibleMediaRows(firstVisiblePost: Int) {
        val lastVisibleAdapter = layoutManager.findLastVisibleItemPosition()
        val lastVisiblePost =
            postIndexAt(lastVisibleAdapter) ?: postIndexAt(lastVisibleAdapter - 1) ?: firstVisiblePost
        val window =
            nativeFeedMediaWindow(
                firstVisiblePost = firstVisiblePost,
                lastVisiblePost = lastVisiblePost,
                postCount = currentPosts.size,
            ) ?: return
        if (window == currentMediaWindow) return
        currentMediaWindow = window
        val rows = buildList {
            for (index in window) {
                val threaded = currentPosts[index].threaded
                addAll(threaded.chain)
                add(threaded.row)
            }
        }
        if (rows.isNotEmpty()) {
            callbacks.onMediaRowsChanged(rows)
        }
    }

    private fun postIndexAt(adapterIndex: Int): Int? =
        postIndexByAdapterIndex.getOrNull(adapterIndex)?.takeIf { it >= 0 }
}

internal fun nativeFeedMediaWindow(
    firstVisiblePost: Int,
    lastVisiblePost: Int,
    postCount: Int,
): IntRange? {
    if (postCount <= 0) return null
    val lastPostIndex = postCount - 1
    val first = firstVisiblePost.coerceIn(0, lastPostIndex)
    val last = lastVisiblePost.coerceIn(first, lastPostIndex)
    return (first - 2).coerceAtLeast(0)..(last + 4).coerceAtMost(lastPostIndex)
}

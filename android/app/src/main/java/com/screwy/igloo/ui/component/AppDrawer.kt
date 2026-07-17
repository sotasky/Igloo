package com.screwy.igloo.ui.component

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.automirrored.filled.Subject
import androidx.compose.material.icons.filled.Bookmark
import androidx.compose.material.icons.filled.DynamicFeed
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.PlayCircle
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.VideoLibrary
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.NavigationDrawerItem
import androidx.compose.material3.NavigationDrawerItemDefaults
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import androidx.navigation.compose.currentBackStackEntryAsState
import com.screwy.igloo.R
import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.data.dao.ChannelReadDao
import com.screwy.igloo.data.entity.ChannelDisplay
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.displayOrName
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.ui.nav.IglooDestination
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.RouteRegistry
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.compose.koinInject

/**
 * Drawer body. Identity row, Liked shortcut on compact layouts and primary
 * navigation on wide layouts, filterable Accounts (Starred / All), then
 * Settings / Logs / Logout. Accounts fill remaining vertical space via
 * `Modifier.weight(1f)` on the LazyColumn.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun AppDrawer(
    navController: NavController,
    onCloseDrawer: () -> Unit,
    onLogoutClick: () -> Unit,
) {
    ModalDrawerSheet(modifier = Modifier.widthIn(max = 320.dp)) {
        AppDrawerContent(
            navController = navController,
            onCloseDrawer = onCloseDrawer,
            onLogoutClick = onLogoutClick,
            dense = false,
            widePrimaryNavigation = false,
        )
    }
}

@Composable
fun PermanentAppSidebar(
    navController: NavController,
    width: Dp,
    onLogoutClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(
        modifier = modifier
            .width(width)
            .fillMaxHeight(),
        color = MaterialTheme.iglooColors.surface,
        tonalElevation = 0.dp,
        shadowElevation = 0.dp,
    ) {
        AppDrawerContent(
            navController = navController,
            onCloseDrawer = {},
            onLogoutClick = onLogoutClick,
            dense = true,
            widePrimaryNavigation = true,
        )
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun AppDrawerContent(
    navController: NavController,
    onCloseDrawer: () -> Unit,
    onLogoutClick: () -> Unit,
    dense: Boolean,
    widePrimaryNavigation: Boolean,
) {
    val authRepo: AuthRepo = koinInject()
    val channelReadDao: ChannelReadDao = koinInject()
    val username by authRepo.usernameFlow.collectAsStateWithLifecycle()
    val channels by channelReadDao.allFlow().collectAsStateWithLifecycle(initialValue = emptyList())
    val colors = MaterialTheme.iglooColors
    val navigator = rememberIglooNavigator(navController, beforeNavigate = onCloseDrawer)
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route
    val currentChannelId = backStackEntry?.arguments?.getString("channel_id")

    var query by remember { mutableStateOf("") }
    val trimmed = query.trim().lowercase()
    // Filter on display-name OR fallback-name OR handle. Handle match lets
    // Latin queries find alternate-script display names via the profile handle.
    // channels.name is also checked separately because for Twitter it's often
    // the handle stored a second time. Rows without a populated handle silently
    // skip the handle check.
    val filtered = remember(channels, trimmed) {
        if (trimmed.isEmpty()) channels
        else channels.filter { row ->
            val shown = row.displayOrName.lowercase()
            val rawName = row.channel.name.lowercase()
            val h = drawerAccountHandleCandidate(row).lowercase()
            shown.contains(trimmed) ||
                rawName.contains(trimmed) ||
                (h.isNotEmpty() && h.contains(trimmed))
        }
    }
    // Starred rows render in their own section above the platform groups;
    // mirrors the web sidebar's "Favourites → YouTube → TikTok → …" order so
    // a starred channel is never duplicated under its platform.
    val starred = filtered.filter { it.isStarred == 1 }
    val unstarred = filtered.filter { it.isStarred != 1 }
    val youtube = unstarred.filter { it.channel.platform == "youtube" }
    val tiktok = unstarred.filter { it.channel.platform == "tiktok" }
    val instagram = unstarred.filter { it.channel.platform == "instagram" }
    val twitter = unstarred.filter { it.channel.platform == "twitter" }
    val knownPlatforms = setOf("youtube", "tiktok", "instagram", "twitter")
    val other = unstarred.filter { it.channel.platform !in knownPlatforms }
    val starredLabel = stringResource(R.string.drawer_starred)
    val otherLabel = stringResource(R.string.drawer_other)
    val youtubeLabel = stringResource(R.string.platform_youtube)
    val tiktokLabel = stringResource(R.string.platform_tiktok)
    val instagramLabel = stringResource(R.string.platform_instagram)
    val xLabel = stringResource(R.string.platform_x)
    val rowModifier = if (dense) Modifier.height(44.dp) else Modifier
    val outerVerticalPadding = if (dense) 8.dp else 12.dp
    val rowSpacing = if (dense) 4.dp else 8.dp
    val primaryDestinations = drawerPrimaryDestinations(widePrimaryNavigation)

    Column(
        modifier = Modifier
            .fillMaxHeight()
            .padding(horizontal = 10.dp, vertical = outerVerticalPadding),
        verticalArrangement = Arrangement.spacedBy(rowSpacing),
    ) {
            Text(
                text = stringResource(R.string.app_name),
                style = MaterialTheme.typography.titleLarge,
                color = colors.onSurface,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
            if (!username.isNullOrBlank()) {
                Text(
                    text = stringResource(R.string.status_signed_in_as, username.orEmpty()),
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurfaceMuted,
                    modifier = Modifier.padding(horizontal = 4.dp),
                )
            }
            HorizontalDivider()

            if (primaryDestinations.isNotEmpty()) {
                primaryDestinations.forEach { destination ->
                    val item = primaryDrawerItem(destination)
                    NavigationDrawerItem(
                        modifier = rowModifier,
                        label = { Text(stringResource(item.labelRes)) },
                        icon = { Icon(item.icon, contentDescription = null) },
                        selected = drawerDestinationSelected(currentRoute, destination),
                        onClick = {
                            navigator.openDestination(destination, IglooNavigationSource.Drawer)
                        },
                        colors = NavigationDrawerItemDefaults.colors(),
                    )
                }

                HorizontalDivider()
            }

            Text(
                text = stringResource(R.string.drawer_accounts).uppercase(),
                style = MaterialTheme.typography.labelSmall,
                color = colors.onSurfaceMuted,
                modifier = Modifier.padding(horizontal = 4.dp, vertical = 4.dp),
            )
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                placeholder = { Text(stringResource(R.string.drawer_search_accounts)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            val onRowClick: (String) -> Unit = { channelId ->
                navigator.openChannel(channelId, IglooNavigationSource.Drawer)
            }
            val listState = rememberLazyListState()
            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth(),
            ) {
                LazyColumn(
                    state = listState,
                    modifier = Modifier.fillMaxSize(),
                ) {
                    platformSection(starredLabel, "starred", starred, currentRoute, currentChannelId, onRowClick)
                    platformSection(youtubeLabel, "youtube", youtube, currentRoute, currentChannelId, onRowClick)
                    platformSection(tiktokLabel, "tiktok", tiktok, currentRoute, currentChannelId, onRowClick)
                    platformSection(instagramLabel, "instagram", instagram, currentRoute, currentChannelId, onRowClick)
                    platformSection(xLabel, "twitter", twitter, currentRoute, currentChannelId, onRowClick)
                    platformSection(otherLabel, "other", other, currentRoute, currentChannelId, onRowClick)
                }
                DrawerScrollbar(state = listState, color = colors.primary)
            }

            HorizontalDivider()
            NavigationDrawerItem(
                modifier = rowModifier,
                label = { Text(stringResource(R.string.settings_screen_title)) },
                icon = { Icon(Icons.Default.Settings, contentDescription = null) },
                selected = drawerDestinationSelected(currentRoute, IglooDestination.Settings),
                onClick = {
                    navigator.openDestination(IglooDestination.Settings, IglooNavigationSource.Drawer)
                },
            )
            NavigationDrawerItem(
                modifier = rowModifier,
                label = { Text(stringResource(R.string.logs_title)) },
                icon = { Icon(Icons.AutoMirrored.Filled.Subject, contentDescription = null) },
                selected = drawerDestinationSelected(currentRoute, IglooDestination.Logs),
                onClick = {
                    navigator.openDestination(IglooDestination.Logs, IglooNavigationSource.Drawer)
                },
            )
            HorizontalDivider()
            NavigationDrawerItem(
                modifier = rowModifier,
                label = { Text(stringResource(R.string.action_logout)) },
                icon = { Icon(Icons.AutoMirrored.Filled.Logout, contentDescription = null) },
                selected = false,
                onClick = onLogoutClick,
            )
        }
}

@Composable
private fun AccountSectionHeader(label: String) {
    val colors = MaterialTheme.iglooColors
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .background(colors.surface)
            .padding(horizontal = 4.dp, vertical = 6.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium,
            color = colors.onSurfaceMuted,
            fontWeight = FontWeight.SemiBold,
        )
    }
}

/**
 * Adds a section header + rows to a [LazyColumn]. Empty sections render
 * nothing so the drawer doesn't leak "TikTok" / "Instagram" headers for
 * platforms with no subscriptions, or headers for sections that were
 * filtered away by the search query. The [keyPrefix] namespaces row keys
 * across sections so starred and platform lists can co-exist without
 * duplicate-key crashes when a starred channel's platform also has
 * unstarred rows.
 */
@OptIn(ExperimentalFoundationApi::class)
private fun LazyListScope.platformSection(
    title: String,
    keyPrefix: String,
    rows: List<ChannelDisplay>,
    currentRoute: String?,
    currentChannelId: String?,
    onChannelClick: (String) -> Unit,
) {
    if (rows.isEmpty()) return
    stickyHeader(key = "hdr-$keyPrefix") { AccountSectionHeader(title) }
    items(items = rows, key = { "$keyPrefix-${it.channel.channelId}" }) { row ->
        AccountRow(
            row = row,
            selected = drawerChannelSelected(currentRoute, currentChannelId, row.channel.channelId),
        ) {
            onChannelClick(row.channel.channelId)
        }
    }
}

private val WideDrawerPrimaryDestinations = listOf(
    IglooDestination.Feed,
    IglooDestination.Videos,
    IglooDestination.Moments,
    IglooDestination.Bookmarks,
    IglooDestination.Liked,
    IglooDestination.Downloaded,
)

private val CompactDrawerPrimaryDestinations = listOf(
    IglooDestination.Liked,
    IglooDestination.Downloaded,
)

private data class PrimaryDrawerItem(
    val labelRes: Int,
    val icon: ImageVector,
)

private fun primaryDrawerItem(destination: IglooDestination): PrimaryDrawerItem = when (destination) {
    IglooDestination.Feed -> PrimaryDrawerItem(R.string.nav_feed, Icons.Default.DynamicFeed)
    IglooDestination.Videos -> PrimaryDrawerItem(R.string.nav_videos, Icons.Default.VideoLibrary)
    IglooDestination.Moments -> PrimaryDrawerItem(R.string.nav_moments, Icons.Default.PlayCircle)
    IglooDestination.Bookmarks -> PrimaryDrawerItem(R.string.nav_bookmarks, Icons.Default.Bookmark)
    IglooDestination.Liked -> PrimaryDrawerItem(R.string.nav_liked, Icons.Default.Favorite)
    IglooDestination.Downloaded -> PrimaryDrawerItem(R.string.nav_downloaded, Icons.Default.Download)
    else -> error("Unsupported primary drawer destination: $destination")
}

internal fun drawerPrimaryDestinations(widePrimaryNavigation: Boolean): List<IglooDestination> =
    if (widePrimaryNavigation) WideDrawerPrimaryDestinations else CompactDrawerPrimaryDestinations

internal fun drawerDestinationSelected(currentRoute: String?, destination: IglooDestination): Boolean {
    val route = currentRoute?.trim().orEmpty()
    return when (destination) {
        IglooDestination.Feed -> route == RouteRegistry.Feed.route
        IglooDestination.Videos -> route == RouteRegistry.Videos.route
        IglooDestination.Moments -> route == RouteRegistry.Moments.route ||
            route == RouteRegistry.AllMoments.route ||
            route == RouteRegistry.Shorts.route
        IglooDestination.Bookmarks -> route == RouteRegistry.Bookmarks.route
        IglooDestination.Liked -> route == RouteRegistry.Liked.route
        IglooDestination.Downloaded -> route == RouteRegistry.Downloaded.route
        IglooDestination.Settings -> route == RouteRegistry.Settings.route || route.startsWith("settings/")
        IglooDestination.Logs -> route == RouteRegistry.Logs.route || route == RouteRegistry.OutboxLogs.route
        else -> false
    }
}

internal fun drawerChannelSelected(
    currentRoute: String?,
    currentChannelId: String?,
    rowChannelId: String,
): Boolean =
    currentRoute == RouteRegistry.Channel.route &&
        currentChannelId?.trim() == rowChannelId

/** Short, user-facing platform name shown next to each row — mirrors the web sidebar. */
@Composable
private fun platformLabel(platform: String?): String = when (platform) {
    "youtube" -> stringResource(R.string.platform_youtube)
    "tiktok" -> stringResource(R.string.platform_tiktok)
    "instagram" -> stringResource(R.string.platform_instagram)
    "twitter" -> stringResource(R.string.platform_x)
    else -> ""
}

/** Color pairs for the platform chip — (text color, background tint). */
private data class ChipColors(val fg: Color, val bg: Color)

private fun chipColors(platform: String?): ChipColors = when (platform) {
    "youtube"   -> ChipColors(Color(0xFFFF5252), Color(0x1FFF5252))
    "tiktok"    -> ChipColors(Color(0xFFF5F5F5), Color(0x14FFFFFF))
    "instagram" -> ChipColors(Color(0xFFE1306C), Color(0x1FE1306C))
    "twitter"   -> ChipColors(Color(0xFFE1E1E1), Color(0x261D9BF0))
    else        -> ChipColors(Color(0xFFAAAAAA), Color(0x14FFFFFF))
}

@Composable
private fun PlatformChip(platform: String?) {
    val label = platformLabel(platform)
    if (label.isEmpty()) return
    val c = chipColors(platform)
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(3.dp))
            .background(c.bg)
            .padding(horizontal = 6.dp, vertical = 2.dp),
    ) {
        Text(
            text = label,
            color = c.fg,
            fontSize = 10.sp,
            fontWeight = FontWeight.SemiBold,
            maxLines = 1,
        )
    }
}

internal fun drawerAccountHandle(row: ChannelDisplay): String =
    drawerAccountHandleCandidate(row)
        .takeIf { shouldShowHandle(row.displayOrName, it) }
        .orEmpty()

private fun drawerAccountHandleCandidate(row: ChannelDisplay): String =
    platformHandleCandidate(row.channel.platform, row.handle)
        .ifBlank { fallbackDrawerHandle(row) }

private fun fallbackDrawerHandle(row: ChannelDisplay): String {
    val channel = row.channel
    if (channel.platform !in handleFirstDrawerPlatforms) return ""
    if (channel.platform == "tiktok") {
            return sequenceOf(
                channel.sourceId,
                stripPlatformPrefix(channel.channelId),
            )
            .map { platformHandleCandidate(channel.platform, it) }
            .firstOrNull { it.isNotBlank() }
            .orEmpty()
    }
    normalizeHandle(channel.sourceId).takeIf { it.isNotBlank() }?.let { return it }
    return normalizeHandle(stripPlatformPrefix(channel.channelId))
}

private val handleFirstDrawerPlatforms = setOf("twitter", "tiktok", "instagram")

@Composable
private fun AccountRow(
    row: ChannelDisplay,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val handle = drawerAccountHandle(row)
    val rowBackground = if (selected) colors.primary.copy(alpha = 0.14f) else Color.Transparent
    val rowContent = if (selected) colors.primary else colors.onSurface
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(rowBackground)
            .clickable { onClick() }
            .padding(horizontal = 4.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Box(modifier = Modifier.size(32.dp).clip(CircleShape)) {
            Avatar(channelId = row.channel.channelId, size = 32.dp, onClick = onClick)
        }
        // Name, handle, and platform chip share a flex:1 sub-row so metadata
        // stays glued to the truncated name instead of floating to the far
        // right next to the star. weight(fill = false) lets text shrink but
        // not grow, so short names keep their natural width.
        Row(
            modifier = Modifier.weight(1f),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text(
                text = row.displayOrName,
                style = MaterialTheme.typography.bodyMedium,
                color = rowContent,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            if (handle.isNotEmpty()) {
                Text(
                    text = "@$handle",
                    style = MaterialTheme.typography.bodySmall,
                    color = colors.onSurfaceHandle,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
            }
            // Always visible on Android (unlike web hover) so users can tell
            // X from TikTok at a glance in the drawer.
            PlatformChip(row.channel.platform)
        }
        if (row.isStarred == 1) {
            Icon(
                imageVector = Icons.Default.Star,
                contentDescription = stringResource(R.string.drawer_starred),
                tint = colors.primary,
                modifier = Modifier.size(16.dp),
            )
            Spacer(Modifier.size(0.dp))
        }
    }
}

/**
 * Tappable / draggable vertical scrollbar overlay for a LazyColumn. Sits on
 * the right edge of its parent [Box] with a 28dp-wide hit zone (big enough
 * for a fingertip) and a 5dp-wide rounded thumb rendered in the theme
 * accent color. Tap anywhere on the rail to jump to that offset; drag to
 * scroll continuously.
 *
 * Two things that needed to be right (previous version got both wrong):
 *
 * - Position source: use `state.firstVisibleItemIndex` + `firstVisibleItemScrollOffset`,
 *   NOT `layout.visibleItemsInfo.firstOrNull()?.index`. The latter includes
 *   the sticky header pinned at the top of the viewport, so with
 *   Starred/YouTube/TikTok/X all using stickyHeader {}, the reported "first
 *   visible index" never advanced past the first sticky header's index —
 *   pinning the thumb near 0% regardless of how far you scrolled.
 *
 * - Scroll API: use `state.scrollBy(pixelDelta)` for drag moves, NOT
 *   `scrollToItem(idx)`. scrollToItem snaps to integer item positions, so a
 *   slow finger drag produces a discrete staircase scroll instead of
 *   tracking the finger continuously.
 *
 * Item height is approximated as `viewportHeight / visibleCount` — fine for
 * the drawer where every row uses the same AccountRow template. Sticky
 * headers are slightly taller and inflate visibleCount a bit, but it's a
 * visual-only approximation; both jump-to-fraction and drag-by-delta
 * express themselves through the same scrollBy delta so any error is
 * consistent and self-correcting.
 */
/**
 * Visual-only scroll position indicator. Deliberately not draggable: past
 * attempts with Modifier.draggable / awaitEachGesture + dispatchRawDelta all
 * fought LazyColumn's own scroll scheduler for the same pointer stream,
 * producing lag. Keep the native finger-on-list swipe, which hooks directly
 * into Compose's scroll plumbing, and only add what was missing:
 *
 *  - a visible thumb showing where in the list you are
 *  - tap-to-jump for quick navigation across 1000+ rows
 *
 * No drag handler on the scrollbar means no competition with the LazyColumn's
 * built-in scroll. To scroll continuously, swipe the list itself.
 */
@Composable
private fun BoxScope.DrawerScrollbar(
    state: LazyListState,
    color: Color,
    hitWidth: androidx.compose.ui.unit.Dp = 32.dp,
    thumbWidth: androidx.compose.ui.unit.Dp = 6.dp,
    minThumbHeightPx: Float = 72f,
) {
    Box(
        modifier = Modifier
            .align(Alignment.CenterEnd)
            .fillMaxHeight()
            .width(hitWidth)
            .pointerInput(state) {
                detectTapGestures { offset ->
                    jumpToFraction(offset.y, size.height.toFloat(), state, minThumbHeightPx)
                }
            }
            .drawWithContent {
                drawContent()
                drawScrollThumb(state, color, thumbWidth.toPx(), minThumbHeightPx)
            }
    )
}

private fun DrawScope.drawScrollThumb(
    state: LazyListState,
    color: Color,
    thumbPx: Float,
    minThumbHeightPx: Float,
) {
    val dims = state.scrollDims(size.height, minThumbHeightPx) ?: return
    val progress = (dims.currentScrollPx / dims.scrollablePx).coerceIn(0f, 1f)
    val trackH = (size.height - dims.thumbH).coerceAtLeast(0f)
    val thumbY = trackH * progress
    drawRoundRect(
        color = color,
        topLeft = Offset(size.width - thumbPx - 4f, thumbY),
        size = Size(thumbPx, dims.thumbH),
        cornerRadius = CornerRadius(thumbPx, thumbPx),
    )
}

/**
 * Absolute jump for the initial tap. `dispatchRawDelta` is synchronous and
 * doesn't animate — perfect for a scrollbar jump where any animation would
 * fight the user's finger.
 */
private fun jumpToFraction(
    tapY: Float,
    trackPx: Float,
    state: LazyListState,
    minThumbHeightPx: Float,
) {
    val dims = state.scrollDims(trackPx, minThumbHeightPx) ?: return
    val effectiveTrack = (trackPx - dims.thumbH).coerceAtLeast(1f)
    // Center thumb on tap so the finger lands in the middle of the thumb.
    val fraction = ((tapY - dims.thumbH / 2f) / effectiveTrack).coerceIn(0f, 1f)
    val targetScrollPx = fraction * dims.scrollablePx
    val delta = targetScrollPx - dims.currentScrollPx
    state.dispatchRawDelta(delta)
}

/**
 * Bundle of scroll dimensions for the scrollbar math. Returns null when the
 * list isn't scrollable (empty, all items fit in viewport, or layout not
 * ready), letting callers short-circuit.
 */
private data class ScrollDims(
    val currentScrollPx: Float,
    val scrollablePx: Float,
    val thumbH: Float,
)

private fun LazyListState.scrollDims(trackPx: Float, minThumbHeightPx: Float): ScrollDims? {
    val layout = layoutInfo
    val total = layout.totalItemsCount
    if (total == 0) return null
    val viewportH = (layout.viewportEndOffset - layout.viewportStartOffset).toFloat()
    if (viewportH <= 0f) return null
    val visibleCount = layout.visibleItemsInfo.size.coerceAtLeast(1)
    if (visibleCount >= total) return null
    val avgItemH = viewportH / visibleCount
    val scrollablePx = (total * avgItemH - viewportH).coerceAtLeast(1f)
    // Use the scroll-position anchor, not visibleItemsInfo — the latter
    // pins at the sticky header's index and would freeze the thumb near 0%.
    val currentScrollPx = firstVisibleItemIndex * avgItemH + firstVisibleItemScrollOffset
    val thumbH = (trackPx * visibleCount / total).coerceAtLeast(minThumbHeightPx)
    return ScrollDims(currentScrollPx, scrollablePx, thumbH)
}

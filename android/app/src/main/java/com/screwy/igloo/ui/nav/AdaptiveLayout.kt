package com.screwy.igloo.ui.nav

import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalConfiguration

internal const val WideLayoutBreakpointDp = 600
internal const val WideSidebarExpandedBreakpointDp = 840
internal const val WideSidebarCompactWidthDp = 240
internal const val WideSidebarExpandedWidthDp = 280
internal const val TimelineContentMaxWidthDp = 760
internal const val GridContentMaxWidthDp = 960
internal const val PlayerContentMaxWidthDp = 960
internal const val MomentsStageMaxWidthDp = 430
internal const val WideVideoGridMinCellWidthDp = 200
internal const val WideVerticalGridMinCellWidthDp = 144
internal const val CompactMomentsShortHeightRatioPermille = 1500

internal enum class IglooLayoutClass {
    Compact,
    Wide,
}

internal enum class WideContentKind {
    Timeline,
    Grid,
    Player,
    MomentsStage,
}

internal data class IglooAdaptiveLayout(
    val layoutClass: IglooLayoutClass,
    val screenWidthDp: Int,
    val screenHeightDp: Int,
) {
    val isWide: Boolean
        get() = layoutClass == IglooLayoutClass.Wide

    val sidebarWidthDp: Int
        get() = wideSidebarWidthDp(screenWidthDp)
}

internal data class MomentsStageSizeDp(
    val widthDp: Int,
    val heightDp: Int,
)

@Composable
internal fun rememberIglooAdaptiveLayout(): IglooAdaptiveLayout {
    val configuration = LocalConfiguration.current
    return IglooAdaptiveLayout(
        layoutClass = iglooLayoutClassForWidthDp(configuration.screenWidthDp),
        screenWidthDp = configuration.screenWidthDp,
        screenHeightDp = configuration.screenHeightDp,
    )
}

internal fun iglooLayoutClassForWidthDp(screenWidthDp: Int): IglooLayoutClass =
    if (screenWidthDp >= WideLayoutBreakpointDp) IglooLayoutClass.Wide else IglooLayoutClass.Compact

internal fun wideSidebarWidthDp(screenWidthDp: Int): Int =
    if (screenWidthDp >= WideSidebarExpandedBreakpointDp) {
        WideSidebarExpandedWidthDp
    } else {
        WideSidebarCompactWidthDp
    }

internal fun wideContentKindForRoute(route: String?): WideContentKind =
    when (route) {
        RouteRegistry.Moments.route,
        RouteRegistry.Shorts.route -> WideContentKind.MomentsStage

        RouteRegistry.Player.route -> WideContentKind.Player

        RouteRegistry.Videos.route,
        RouteRegistry.Bookmarks.route,
        RouteRegistry.Downloaded.route,
        RouteRegistry.Channel.route,
        RouteRegistry.AllMoments.route -> WideContentKind.Grid

        else -> WideContentKind.Timeline
    }

internal fun wideContentMaxWidthDp(kind: WideContentKind): Int =
    when (kind) {
        WideContentKind.Timeline -> TimelineContentMaxWidthDp
        WideContentKind.Grid -> GridContentMaxWidthDp
        WideContentKind.Player -> PlayerContentMaxWidthDp
        WideContentKind.MomentsStage -> MomentsStageMaxWidthDp
    }

internal fun wideMomentsStageSizeDp(
    availableWidthDp: Int,
    availableHeightDp: Int,
    maxWidthDp: Int = MomentsStageMaxWidthDp,
): MomentsStageSizeDp {
    val safeWidth = availableWidthDp.coerceAtLeast(1)
    val safeHeight = availableHeightDp.coerceAtLeast(1)
    val widthFromHeight = safeHeight * 9 / 16
    val width = minOf(safeWidth, maxWidthDp, widthFromHeight.coerceAtLeast(1))
        .coerceAtLeast(1)
    val height = (width * 16 / 9).coerceAtMost(safeHeight).coerceAtLeast(1)
    return MomentsStageSizeDp(widthDp = width, heightDp = height)
}

internal fun compactMomentsStageSizeDp(
    availableWidthDp: Int,
    availableHeightDp: Int,
): MomentsStageSizeDp {
    val safeWidth = availableWidthDp.coerceAtLeast(1)
    val safeHeight = availableHeightDp.coerceAtLeast(1)
    val shortHeight = safeHeight * 1000 < safeWidth * CompactMomentsShortHeightRatioPermille
    if (!shortHeight) return MomentsStageSizeDp(widthDp = safeWidth, heightDp = safeHeight)
    return wideMomentsStageSizeDp(
        availableWidthDp = safeWidth,
        availableHeightDp = safeHeight,
        maxWidthDp = safeWidth,
    )
}

internal fun routeUsesWideSidebar(route: String?): Boolean =
    RouteRegistry.find(route)?.chrome?.wideDrawerChrome == DrawerChrome.Enabled

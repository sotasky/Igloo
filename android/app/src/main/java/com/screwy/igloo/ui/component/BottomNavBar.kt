package com.screwy.igloo.ui.component

import androidx.annotation.StringRes
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bookmark
import androidx.compose.material.icons.filled.DynamicFeed
import androidx.compose.material.icons.filled.PlayCircle
import androidx.compose.material.icons.filled.VideoLibrary
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.navigation.NavController
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.currentBackStackEntryAsState
import com.screwy.igloo.R
import com.screwy.igloo.ui.nav.RouteRegistry
import com.screwy.igloo.ui.theme.iglooColors

private data class NavTab(
    val route: String,
    @param:StringRes val labelRes: Int,
    val icon: ImageVector,
)

private val TABS = listOf(
    NavTab(RouteRegistry.Feed.route, R.string.nav_feed, Icons.Default.DynamicFeed),
    NavTab(RouteRegistry.Videos.route, R.string.nav_videos, Icons.Default.VideoLibrary),
    NavTab(RouteRegistry.Moments.route, R.string.nav_moments, Icons.Default.PlayCircle),
    NavTab(RouteRegistry.Bookmarks.route, R.string.nav_bookmarks, Icons.Default.Bookmark),
)

/**
 * Four-tab Material3 `NavigationBar`. Tab selection tracks the back stack
 * (including the nested `moments-graph`), so the Moments tab stays lit while
 * `all-moments` is on top. Taps use saveState/restoreState round-tripping.
 */
@Composable
fun BottomNavBar(
    navController: NavController,
) {
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentDestination = backStackEntry?.destination

    NavigationBar {
        TABS.forEach { tab ->
            val selected = currentDestination?.hierarchy?.any { it.route == tab.route } == true
            val label = stringResource(tab.labelRes)
            NavigationBarItem(
                selected = selected,
                onClick = {
                    // Pop back to the start destination without saveState so non-tab
                    // routes (Settings/Logs/Channel/Player) left on top of a tab
                    // aren't silently restored the next time a tab is re-tapped.
                    // Personal-tool ergonomics > cross-tab scroll preservation.
                    navController.navigate(tab.route) {
                        popUpTo(navController.graph.findStartDestination().id) {
                            inclusive = false
                        }
                        launchSingleTop = true
                    }
                },
                icon = { Icon(tab.icon, contentDescription = label) },
                label = { Text(label) },
                colors = NavigationBarItemDefaults.colors(
                    selectedIconColor = MaterialTheme.iglooColors.primary,
                    selectedTextColor = MaterialTheme.iglooColors.primary,
                    indicatorColor = MaterialTheme.iglooColors.primary.copy(alpha = 0.16f),
                ),
            )
        }
    }
}

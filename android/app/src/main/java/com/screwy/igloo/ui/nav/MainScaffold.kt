package com.screwy.igloo.ui.nav

import android.graphics.Rect
import android.view.View
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material3.rememberTopAppBarState
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.view.ViewCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import androidx.navigation.compose.currentBackStackEntryAsState
import com.screwy.igloo.R
import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.auth.LogoutReason
import com.screwy.igloo.data.dao.ChannelDao
import com.screwy.igloo.data.dao.ChannelProfileDao
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.component.AppDrawer
import com.screwy.igloo.ui.component.BottomNavBar
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.launch
import org.koin.compose.koinInject

/**
 * Runtime handle for overlay-driven chrome changes inside [MainScaffold].
 * Routes keep their base chrome in [RouteChromePolicy]; transient overlays
 * acquire a state here so they cannot leave the top bar stuck hidden after
 * navigation or recomposition.
 */
class OverlayChromeController internal constructor() {
    private val hiddenTopBarCount = mutableStateOf(0)
    private val hiddenBottomNavCount = mutableStateOf(0)

    val state: OverlayChromeState
        get() = when {
            hiddenTopBarCount.value > 0 && hiddenBottomNavCount.value > 0 -> OverlayChromeState.FullscreenMedia
            hiddenTopBarCount.value > 0 -> OverlayChromeState.HideTopBar
            else -> OverlayChromeState.None
        }

    internal fun acquire(state: OverlayChromeState) {
        if (state.hidesScaffoldTopBar) hiddenTopBarCount.value = hiddenTopBarCount.value + 1
        if (state.hidesBottomNav) hiddenBottomNavCount.value = hiddenBottomNavCount.value + 1
    }

    internal fun release(state: OverlayChromeState) {
        if (state.hidesScaffoldTopBar) {
            hiddenTopBarCount.value = (hiddenTopBarCount.value - 1).coerceAtLeast(0)
        }
        if (state.hidesBottomNav) {
            hiddenBottomNavCount.value = (hiddenBottomNavCount.value - 1).coerceAtLeast(0)
        }
    }
}

val LocalOverlayChromeController = compositionLocalOf<OverlayChromeController> {
    error("OverlayChromeController not provided — wrap your composable in MainScaffold")
}

class DrawerController internal constructor(
    private val openDrawer: () -> Unit,
) {
    fun open() = openDrawer()
}

val LocalDrawerController = compositionLocalOf {
    DrawerController(openDrawer = {})
}

/**
 * Apply transient overlay chrome while [state] is active. Guarantees the state
 * is released on leave-composition so navigation away from the hosting route
 * never leaves a stuck hidden state.
 */
@Composable
fun ApplyOverlayChrome(state: OverlayChromeState) {
    val controller = LocalOverlayChromeController.current
    DisposableEffect(controller, state) {
        controller.acquire(state)
        onDispose { controller.release(state) }
    }
}

/**
 * Scaffold for routes that want the drawer + bottom bar + global UiEffects handling.
 * Login and the fullscreen Player route opt out (render their composable directly).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScaffold(
    navController: NavController,
    content: @Composable () -> Unit,
) {
    val uiEffects: UiEffects = koinInject()
    val authRepo: AuthRepo = koinInject()
    val channelDao: ChannelDao = koinInject()
    val channelProfileDao: ChannelProfileDao = koinInject()
    val context = LocalContext.current
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val snackbarHostState = remember { SnackbarHostState() }
    val coroutineScope = rememberCoroutineScope()
    val overlayChromeController = remember { OverlayChromeController() }
    val topBarState = rememberTopAppBarState()
    val topBarScrollBehavior = TopAppBarDefaults.enterAlwaysScrollBehavior(topBarState)
    val edgeDrawerWidth = 56.dp
    val edgeDrawerExclusionPx = with(LocalDensity.current) { edgeDrawerWidth.roundToPx() }
    val rootView = LocalView.current
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route
    val chromePolicy = routeChromePolicyFor(currentRoute)
    val drawerEnabled = chromePolicy.drawerChrome == DrawerChrome.Enabled
    val channelId = backStackEntry?.arguments?.getString("channel_id")
    val emptyChannelState = remember { mutableStateOf<com.screwy.igloo.data.entity.ChannelEntity?>(null) }
    val emptyProfileState = remember { mutableStateOf<com.screwy.igloo.data.entity.ChannelProfileEntity?>(null) }
    val channelState: State<com.screwy.igloo.data.entity.ChannelEntity?> = channelId
        ?.takeIf { it.isNotBlank() }
        ?.let { channelDao.getByIdFlow(it).collectAsStateWithLifecycle(initialValue = null) }
        ?: emptyChannelState
    val channelProfileState: State<com.screwy.igloo.data.entity.ChannelProfileEntity?> = channelId
        ?.takeIf { it.isNotBlank() }
        ?.let { channelProfileDao.getByIdFlow(it).collectAsStateWithLifecycle(initialValue = null) }
        ?: emptyProfileState
    val channel by channelState
    val channelProfile by channelProfileState

    fun openDrawer() {
        if (!drawerEnabled) return
        coroutineScope.launch {
            drawerState.open()
        }
    }
    val drawerController = remember { DrawerController(::openDrawer) }

    DisposableEffect(rootView, edgeDrawerExclusionPx) {
        fun updateExclusion(view: View) {
            ViewCompat.setSystemGestureExclusionRects(
                view,
                listOf(Rect(0, 0, edgeDrawerExclusionPx, view.height)),
            )
        }

        val listener = View.OnLayoutChangeListener { view, _, _, _, _, _, _, _, _ ->
            updateExclusion(view)
        }
        rootView.addOnLayoutChangeListener(listener)
        updateExclusion(rootView)

        onDispose {
            rootView.removeOnLayoutChangeListener(listener)
            ViewCompat.setSystemGestureExclusionRects(rootView, emptyList())
        }
    }

    val suppressTopBar: Boolean = !chromePolicy.usesScaffoldTopBar ||
        overlayChromeController.state.hidesScaffoldTopBar
    val topBarTitle = when (val title = chromePolicy.topBarTitle) {
        is TopBarTitle.Static -> title.value
        is TopBarTitle.Resource -> stringResource(title.id)
        TopBarTitle.Channel -> channelProfile?.displayName
            ?.takeIf { it.isNotBlank() }
            ?: channel?.name?.takeIf { it.isNotBlank() }
            ?: channelProfile?.handle?.takeIf { it.isNotBlank() }
            ?: stringResource(R.string.label_channel)
        TopBarTitle.None -> null
    }

    LaunchedEffect(Unit) {
        uiEffects.flow.collect { effect ->
            when (effect) {
                is UiEffect.Toast -> {
                    snackbarHostState.showSnackbar(
                        message = effect.message,
                        duration = if (effect.longDuration) SnackbarDuration.Long else SnackbarDuration.Short,
                    )
                }
                is UiEffect.ToastRes -> {
                    val message = if (effect.formatArgs.isEmpty()) {
                        context.getString(effect.resId)
                    } else {
                        context.getString(effect.resId, *effect.formatArgs.toTypedArray())
                    }
                    snackbarHostState.showSnackbar(
                        message = message,
                        duration = if (effect.longDuration) SnackbarDuration.Long else SnackbarDuration.Short,
                    )
                }
                is UiEffect.DialogError -> {
                    snackbarHostState.showSnackbar(
                        message = "${effect.title}: ${effect.body}",
                        duration = SnackbarDuration.Long,
                    )
                }
                is UiEffect.NavigateTo,
                UiEffect.RequireLogin -> Unit
            }
        }
    }

    ModalNavigationDrawer(
        drawerState = drawerState,
        gesturesEnabled = drawerEnabled,
        drawerContent = {
            AppDrawer(
                navController = navController,
                onCloseDrawer = { coroutineScope.launch { drawerState.close() } },
                onLogoutClick = {
                    coroutineScope.launch {
                        drawerState.close()
                        authRepo.logout(LogoutReason.UserInitiated)
                        navController.navigate(RouteRegistry.Login.route) {
                            popUpTo(navController.graph.id) { inclusive = true }
                        }
                    }
                },
            )
        },
    ) {
        Scaffold(
            modifier = if (!suppressTopBar && chromePolicy.usesScaffoldTopBar) {
                Modifier.nestedScroll(topBarScrollBehavior.nestedScrollConnection)
            } else {
                Modifier
            },
            contentWindowInsets = WindowInsets(0, 0, 0, 0),
            topBar = {
                if (!suppressTopBar) {
                    TopAppBar(
                        title = {
                            if (!topBarTitle.isNullOrBlank()) {
                                Text(
                                    text = topBarTitle,
                                    maxLines = 1,
                                )
                            }
                        },
                        navigationIcon = {
                            if (drawerEnabled) {
                                IconButton(onClick = ::openDrawer) {
                                    Icon(
                                        imageVector = Icons.Default.Menu,
                                        contentDescription = stringResource(R.string.action_open_drawer),
                                    )
                                }
                            } else {
                                IconButton(onClick = { navController.popBackStack() }) {
                                    Icon(
                                        imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                                        contentDescription = stringResource(R.string.action_back),
                                    )
                                }
                            }
                        },
                        colors = TopAppBarDefaults.topAppBarColors(
                            containerColor = MaterialTheme.iglooColors.surface,
                            titleContentColor = MaterialTheme.iglooColors.onSurface,
                            navigationIconContentColor = MaterialTheme.iglooColors.onSurface,
                        ),
                        scrollBehavior = topBarScrollBehavior,
                    )
                }
            },
            bottomBar = {
                if (chromePolicy.showsBottomNav && !overlayChromeController.state.hidesBottomNav) {
                    BottomNavBar(navController = navController)
                }
            },
            snackbarHost = { SnackbarHost(snackbarHostState) },
        ) { paddingValues ->
            Box(
                modifier = Modifier
                    .padding(paddingValues)
                    .fillMaxSize(),
            ) {
                CompositionLocalProvider(
                    LocalOverlayChromeController provides overlayChromeController,
                    LocalDrawerController provides drawerController,
                ) {
                    content()
                }
            }
        }
    }
}

export const CINEMA_MIN_PLAYER_WIDTH = 720
export const CINEMA_HIDE_LEFT_SIDEBAR_WIDTH = 800
export const CINEMA_COMPACT_LEFT_SIDEBAR_WIDTH = 1000
export const PLAYER_SIDEBAR_WIDTH = 320
export const PLAYER_MAIN_HORIZONTAL_PADDING = 48

export function shouldAutoEnableCinema(layoutWidth, sidebarIsStacked) {
  if (sidebarIsStacked) return false
  return layoutWidth - PLAYER_SIDEBAR_WIDTH - PLAYER_MAIN_HORIZONTAL_PADDING < CINEMA_MIN_PLAYER_WIDTH
}

export function cinemaSidebarDefaultMode(layoutWidth) {
  if (layoutWidth >= CINEMA_COMPACT_LEFT_SIDEBAR_WIDTH) return null
  if (layoutWidth >= CINEMA_HIDE_LEFT_SIDEBAR_WIDTH) return 'compact'
  return 'hidden'
}

export function initCinemaView({ root, button, onCinemaRequested }) {
  const sidebar = root && root.querySelector('.player-sidebar')
  if (!root || !button || !sidebar) return

  const stackedSidebar = window.matchMedia('(max-width: 1024px)')
  let manualChoice = null
  let suspendedForFullscreen = false

  function sidebarDefaultMode() {
    return cinemaSidebarDefaultMode(root.getBoundingClientRect().width)
  }

  function setCinemaView(enabled, defaultSidebarMode, forceSidebarMode, notifySidebar) {
    const changed = root.classList.contains('cinema-view') !== enabled
    const hidesPlayerSidebar = enabled && !stackedSidebar.matches
    root.classList.toggle('cinema-view', enabled)
    root.classList.toggle('cinema-hides-player-sidebar', hidesPlayerSidebar)
    if (changed && notifySidebar !== false && typeof CustomEvent === 'function' && typeof root.dispatchEvent === 'function') {
      root.dispatchEvent(new CustomEvent('igloo:cinema-sidebar-change', {
        bubbles: true,
        detail: {
          enabled,
          defaultSidebarMode: enabled ? defaultSidebarMode : null,
          forceSidebarMode: enabled && !!forceSidebarMode,
        },
      }))
    }
    sidebar.setAttribute('aria-hidden', hidesPlayerSidebar ? 'true' : 'false')
    button.setAttribute('aria-pressed', enabled ? 'true' : 'false')
  }

  function recommendedCinemaView() {
    return shouldAutoEnableCinema(root.getBoundingClientRect().width, stackedSidebar.matches)
  }

  function syncCinemaView() {
    if (suspendedForFullscreen) return
    const recommendation = recommendedCinemaView()
    setCinemaView(
      manualChoice === null ? recommendation : manualChoice,
      manualChoice === null && recommendation ? sidebarDefaultMode() : null,
      false,
    )
  }

  button.addEventListener('click', function () {
    const wasEnabled = root.classList.contains('cinema-view')
    const enabled = !wasEnabled
    if (typeof onCinemaRequested === 'function' && onCinemaRequested(enabled)) return
    manualChoice = enabled
    const defaultSidebarMode = enabled && !stackedSidebar.matches ? sidebarDefaultMode() : null
    setCinemaView(enabled, defaultSidebarMode, defaultSidebarMode !== null)
  })

  if (typeof window.ResizeObserver === 'function') {
    new window.ResizeObserver(syncCinemaView).observe(root)
  } else {
    window.addEventListener('resize', syncCinemaView)
  }
  stackedSidebar.addEventListener('change', syncCinemaView)
  syncCinemaView()

  return {
    suspendForFullscreen() {
      const wasEnabled = root.classList.contains('cinema-view')
      suspendedForFullscreen = true
      setCinemaView(false, null, false, false)
      return wasEnabled
    },
    restoreAfterFullscreen(enabled) {
      suspendedForFullscreen = false
      setCinemaView(enabled, null, false, false)
    },
  }
}

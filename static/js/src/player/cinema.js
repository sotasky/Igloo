export const CINEMA_MIN_PLAYER_WIDTH = 720
export const PLAYER_SIDEBAR_WIDTH = 320
export const PLAYER_MAIN_HORIZONTAL_PADDING = 48

export function shouldAutoEnableCinema(layoutWidth, sidebarIsStacked) {
  if (sidebarIsStacked) return false
  return layoutWidth - PLAYER_SIDEBAR_WIDTH - PLAYER_MAIN_HORIZONTAL_PADDING < CINEMA_MIN_PLAYER_WIDTH
}

export function initCinemaView({ root, button, onCinemaRequested }) {
  const sidebar = root && root.querySelector('.player-sidebar')
  if (!root || !button || !sidebar) return

  const stackedSidebar = window.matchMedia('(max-width: 1024px)')
  let manualChoice = null
  let suspendedForFullscreen = false

  function setCinemaView(enabled, defaultSidebarMode, notifySidebar) {
    const changed = root.classList.contains('cinema-view') !== enabled
    root.classList.toggle('cinema-view', enabled)
    if (changed && notifySidebar !== false && typeof CustomEvent === 'function' && typeof root.dispatchEvent === 'function') {
      root.dispatchEvent(new CustomEvent('igloo:cinema-sidebar-change', {
        bubbles: true,
        detail: { enabled, defaultSidebarMode: enabled ? defaultSidebarMode : null },
      }))
    }
    sidebar.setAttribute('aria-hidden', enabled ? 'true' : 'false')
    button.classList.toggle('active', enabled)
    button.setAttribute('aria-pressed', enabled ? 'true' : 'false')
  }

  function recommendedCinemaView() {
    return shouldAutoEnableCinema(root.getBoundingClientRect().width, stackedSidebar.matches)
  }

  function syncCinemaView() {
    if (suspendedForFullscreen) return
    const recommendation = recommendedCinemaView()
    const defaultSidebarMode = root.getBoundingClientRect().width < CINEMA_MIN_PLAYER_WIDTH
      ? 'hidden'
      : 'compact'
    setCinemaView(
      manualChoice === null ? recommendation : manualChoice,
      manualChoice === null && recommendation ? defaultSidebarMode : null,
    )
  }

  button.addEventListener('click', function () {
    const wasEnabled = root.classList.contains('cinema-view')
    const enabled = !wasEnabled
    if (typeof onCinemaRequested === 'function' && onCinemaRequested(enabled)) return
    manualChoice = enabled
    setCinemaView(enabled)
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
      setCinemaView(false, null, false)
      return wasEnabled
    },
    restoreAfterFullscreen(enabled) {
      suspendedForFullscreen = false
      setCinemaView(enabled, null, false)
    },
  }
}

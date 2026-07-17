export const CINEMA_TARGET_PLAYER_WIDTH = 1000
export const CINEMA_MIN_PLAYER_WIDTH = CINEMA_TARGET_PLAYER_WIDTH
export const PLAYER_SIDEBAR_WIDTH = 320
export const PLAYER_MAIN_HORIZONTAL_PADDING = 48
export const SIDEBAR_COMPACT_WIDTH = 72

export function shouldAutoEnableCinema(layoutWidth, sidebarIsStacked) {
  if (sidebarIsStacked) return false
  return layoutWidth - PLAYER_SIDEBAR_WIDTH - PLAYER_MAIN_HORIZONTAL_PADDING < CINEMA_MIN_PLAYER_WIDTH
}

export function cinemaLeftSidebarMode(viewportWidth, sidebarWidth, cinemaEnabled, desktopSidebarVisible) {
  if (!cinemaEnabled || !desktopSidebarVisible) return false
  if (viewportWidth - sidebarWidth - PLAYER_MAIN_HORIZONTAL_PADDING >= CINEMA_TARGET_PLAYER_WIDTH) {
    return 'full'
  }
  if (viewportWidth - SIDEBAR_COMPACT_WIDTH - PLAYER_MAIN_HORIZONTAL_PADDING >= CINEMA_TARGET_PLAYER_WIDTH) {
    return 'compact'
  }
  return 'hidden'
}

export function initCinemaView({ root, button }) {
  const sidebar = root && root.querySelector('.player-sidebar')
  if (!root || !button || !sidebar) return

  const stackedSidebar = window.matchMedia('(max-width: 1024px)')
  const desktopSidebar = window.matchMedia('(min-width: 769px)')
  const appSidebar = root.ownerDocument && root.ownerDocument.getElementById('app-sidebar')
  let manualChoice = null
  let normalSidebarWidth = appSidebar ? appSidebar.getBoundingClientRect().width : 0

  function captureNormalSidebarWidth() {
    if (!desktopSidebar.matches || !appSidebar) return
    if (root.classList.contains('cinema-left-sidebar-compact') || root.classList.contains('cinema-left-sidebar-hidden')) return
    normalSidebarWidth = appSidebar.getBoundingClientRect().width
  }

  function normalLayoutWidth() {
    captureNormalSidebarWidth()
    if (desktopSidebar.matches && appSidebar) {
      return window.innerWidth - normalSidebarWidth
    }
    return root.getBoundingClientRect().width
  }

  function setCinemaView(enabled) {
    captureNormalSidebarWidth()
    root.classList.toggle('cinema-view', enabled)
    const leftSidebarMode = cinemaLeftSidebarMode(
      window.innerWidth,
      normalSidebarWidth,
      enabled,
      desktopSidebar.matches && !!appSidebar,
    )
    const leftSidebarHidden = leftSidebarMode === 'hidden'
    root.classList.toggle('cinema-left-sidebar-compact', leftSidebarMode === 'compact')
    root.classList.toggle('cinema-left-sidebar-hidden', leftSidebarHidden)
    if (typeof CustomEvent === 'function' && typeof root.dispatchEvent === 'function') {
      root.dispatchEvent(new CustomEvent('igloo:cinema-sidebar-change', {
        bubbles: true,
        detail: { leftSidebarHidden, leftSidebarMode },
      }))
    }
    sidebar.setAttribute('aria-hidden', enabled ? 'true' : 'false')
    button.classList.toggle('active', enabled)
    button.setAttribute('aria-pressed', enabled ? 'true' : 'false')
  }

  function recommendedCinemaView() {
    return shouldAutoEnableCinema(normalLayoutWidth(), stackedSidebar.matches)
  }

  function syncCinemaView() {
    const recommendation = recommendedCinemaView()
    setCinemaView(manualChoice === null ? recommendation : manualChoice)
  }

  button.addEventListener('click', function () {
    manualChoice = !root.classList.contains('cinema-view')
    setCinemaView(manualChoice)
  })

  if (typeof window.ResizeObserver === 'function') {
    new window.ResizeObserver(syncCinemaView).observe(root)
  } else {
    window.addEventListener('resize', syncCinemaView)
  }
  stackedSidebar.addEventListener('change', syncCinemaView)
  desktopSidebar.addEventListener('change', syncCinemaView)
  syncCinemaView()
}

export const CINEMA_TARGET_PLAYER_WIDTH = 1200
export const CINEMA_MIN_PLAYER_WIDTH = CINEMA_TARGET_PLAYER_WIDTH
export const PLAYER_SIDEBAR_WIDTH = 320
export const PLAYER_MAIN_HORIZONTAL_PADDING = 48

export function shouldAutoEnableCinema(layoutWidth, sidebarIsStacked) {
  if (sidebarIsStacked) return false
  return layoutWidth - PLAYER_SIDEBAR_WIDTH - PLAYER_MAIN_HORIZONTAL_PADDING < CINEMA_MIN_PLAYER_WIDTH
}

export function shouldHideLeftSidebarForCinema(layoutWidth, cinemaEnabled, desktopSidebarVisible) {
  if (!cinemaEnabled || !desktopSidebarVisible) return false
  return layoutWidth - PLAYER_MAIN_HORIZONTAL_PADDING < CINEMA_TARGET_PLAYER_WIDTH
}

export function initCinemaView({ root, button }) {
  const sidebar = root && root.querySelector('.player-sidebar')
  if (!root || !button || !sidebar) return

  const stackedSidebar = window.matchMedia('(max-width: 1024px)')
  const desktopSidebar = window.matchMedia('(min-width: 769px)')
  const appSidebar = root.ownerDocument && root.ownerDocument.getElementById('app-sidebar')
  let manualChoice = null

  function normalLayoutWidth() {
    if (desktopSidebar.matches && appSidebar) {
      return window.innerWidth - appSidebar.getBoundingClientRect().width
    }
    return root.getBoundingClientRect().width
  }

  function setCinemaView(enabled) {
    root.classList.toggle('cinema-view', enabled)
    const leftSidebarHidden = shouldHideLeftSidebarForCinema(normalLayoutWidth(), enabled, desktopSidebar.matches)
    root.classList.toggle(
      'cinema-left-sidebar-hidden',
      leftSidebarHidden,
    )
    if (typeof CustomEvent === 'function' && typeof root.dispatchEvent === 'function') {
      root.dispatchEvent(new CustomEvent('igloo:cinema-sidebar-change', {
        bubbles: true,
        detail: { leftSidebarHidden },
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

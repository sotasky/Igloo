export const CINEMA_MIN_PLAYER_WIDTH = 720
export const PLAYER_SIDEBAR_WIDTH = 320
export const PLAYER_MAIN_HORIZONTAL_PADDING = 48

export function shouldAutoEnableCinema(layoutWidth, sidebarIsStacked) {
  if (sidebarIsStacked) return false
  return layoutWidth - PLAYER_SIDEBAR_WIDTH - PLAYER_MAIN_HORIZONTAL_PADDING < CINEMA_MIN_PLAYER_WIDTH
}

export function initCinemaView({ root, button }) {
  const sidebar = root && root.querySelector('.player-sidebar')
  if (!root || !button || !sidebar) return

  const stackedSidebar = window.matchMedia('(max-width: 1024px)')
  let manualChoice = null
  let lastRecommendation = null

  function setCinemaView(enabled) {
    root.classList.toggle('cinema-view', enabled)
    sidebar.setAttribute('aria-hidden', enabled ? 'true' : 'false')
    button.classList.toggle('active', enabled)
    button.setAttribute('aria-pressed', enabled ? 'true' : 'false')
  }

  function recommendedCinemaView() {
    return shouldAutoEnableCinema(root.getBoundingClientRect().width, stackedSidebar.matches)
  }

  function syncCinemaView() {
    const recommendation = recommendedCinemaView()
    if (lastRecommendation !== null && recommendation !== lastRecommendation) {
      manualChoice = null
    }
    lastRecommendation = recommendation
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
  syncCinemaView()
}

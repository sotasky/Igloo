package com.screwy.igloo.net

import android.content.Context
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.util.Log
import java.net.InetAddress

/**
 * Keeps Igloo-server traffic on the LAN when the configured server host is a private
 * address. Some Android builds can route an app UID over mobile even while shell / adb
 * traffic reaches the home-server subnet over Wi-Fi; binding before server requests makes
 * the routing choice explicit without changing the sync contract.
 */
class LanServerNetworkBinder(
    context: Context,
    private val hostProvider: IglooHostProvider,
) {
    private val connectivity =
        context.applicationContext.getSystemService(ConnectivityManager::class.java)

    @Volatile private var boundHost: String? = null
    @Volatile private var boundNetwork: Network? = null
    @Volatile private var lastNoticeKey: String? = null
    private val wifiNetworksLock = Any()
    private val wifiNetworks = LinkedHashMap<Network, NetworkSnapshot>()

    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            updateWifiNetwork(network) { it }
        }

        override fun onCapabilitiesChanged(
            network: Network,
            networkCapabilities: NetworkCapabilities,
        ) {
            updateWifiNetwork(network) { it.copy(capabilities = networkCapabilities) }
        }

        override fun onLinkPropertiesChanged(
            network: Network,
            linkProperties: LinkProperties,
        ) {
            updateWifiNetwork(network) { it.copy(linkProperties = linkProperties) }
        }

        override fun onLost(network: Network) {
            synchronized(wifiNetworksLock) {
                wifiNetworks.remove(network)
            }
            if (boundNetwork == network) {
                unbindIfNeeded()
            }
        }
    }

    init {
        registerWifiNetworkCallback()
    }

    fun bindForCurrentServerIfNeeded() {
        val host = hostProvider.hostSync().trim().lowercase()
        if (host.isEmpty()) return

        val addresses = runCatching { InetAddress.getAllByName(host).toList() }
            .getOrElse { error ->
                unbindIfNeeded()
                logNotice(
                    key = "resolve_failed:$host:${error::class.simpleName}",
                    message = "server_wifi_bind_failed host=$host reason=resolve_failed error=$error",
                )
                return
            }

        if (addresses.none { it.isLanAddress() }) {
            unbindIfNeeded()
            return
        }

        val network = findWifiNetworkFor(addresses)
        if (network == null) {
            unbindIfNeeded()
            logNotice(
                key = "wifi_route_missing:$host",
                message = "server_wifi_bind_failed host=$host reason=wifi_route_missing",
            )
            return
        }

        if (boundHost == host && boundNetwork == network) return

        val bound = runCatching { connectivity.bindProcessToNetwork(network) }
            .getOrElse { error ->
                logNotice(
                    key = "bind_exception:$host:${error::class.simpleName}",
                    message = "server_wifi_bind_failed host=$host reason=bind_exception error=$error",
                )
                return
            }

        if (bound) {
            boundHost = host
            boundNetwork = network
            lastNoticeKey = null
            Log.i(TAG, "server_wifi_bind host=$host network=$network")
        } else {
            logNotice(
                key = "bind_rejected:$host",
                message = "server_wifi_bind_failed host=$host reason=bind_rejected",
            )
        }
    }

    private fun findWifiNetworkFor(addresses: List<InetAddress>): Network? {
        val networks = synchronized(wifiNetworksLock) {
            wifiNetworks.entries.map { it.key to it.value }
        }
        return networks.firstOrNull { (_, snapshot) ->
            val capabilities = snapshot.capabilities ?: return@firstOrNull false
            if (!capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                return@firstOrNull false
            }
            val links = snapshot.linkProperties ?: return@firstOrNull false
            addresses.any { address -> links.canRouteTo(address) }
        }?.first
    }

    private fun registerWifiNetworkCallback() {
        val request = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .build()
        runCatching {
            connectivity.registerNetworkCallback(request, networkCallback)
        }.onFailure { error ->
            logNotice(
                key = "network_callback_failed:${error::class.simpleName}",
                message = "server_wifi_bind_failed reason=network_callback_failed error=$error",
            )
        }
    }

    private fun updateWifiNetwork(
        network: Network,
        transform: (NetworkSnapshot) -> NetworkSnapshot,
    ) {
        synchronized(wifiNetworksLock) {
            val current = wifiNetworks[network] ?: NetworkSnapshot()
            wifiNetworks[network] = transform(current)
        }
    }

    private fun unbindIfNeeded() {
        if (boundNetwork == null && boundHost == null) return
        runCatching { connectivity.bindProcessToNetwork(null) }
        boundNetwork = null
        boundHost = null
    }

    private fun logNotice(key: String, message: String) {
        if (lastNoticeKey == key) return
        lastNoticeKey = key
        Log.w(TAG, message)
    }

    private fun LinkProperties.canRouteTo(address: InetAddress): Boolean {
        return routes.any { route ->
            runCatching { route.matches(address) }.getOrDefault(false)
        }
    }

    private fun InetAddress.isLanAddress(): Boolean {
        return isAnyLocalAddress ||
            isLoopbackAddress ||
            isLinkLocalAddress ||
            isSiteLocalAddress
    }

    private data class NetworkSnapshot(
        val capabilities: NetworkCapabilities? = null,
        val linkProperties: LinkProperties? = null,
    )

    private companion object {
        const val TAG = "Igloo/Net"
    }
}

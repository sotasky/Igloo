package com.screwy.igloo.auth

/** Test double — all reads / writes stay in a single `HashMap`. */
class InMemoryAuthStorage : AuthStorage {

    private val map = HashMap<String, Any>()

    override fun getString(key: String): String? = map[key] as? String
    override fun getLong(key: String): Long? = map[key] as? Long
    override fun getBoolean(key: String): Boolean? = map[key] as? Boolean

    override fun edit(block: AuthStorage.Editor.() -> Unit) {
        val editor = object : AuthStorage.Editor {
            override fun putString(key: String, value: String?) {
                if (value == null) map.remove(key) else map[key] = value
            }
            override fun putLong(key: String, value: Long) { map[key] = value }
            override fun putBoolean(key: String, value: Boolean) { map[key] = value }
            override fun remove(key: String) { map.remove(key) }
        }
        editor.block()
    }

    override fun clearAll() { map.clear() }

    fun snapshot(): Map<String, Any> = map.toMap()
}

package com.screwy.igloo.channel

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ChannelRouteTest {

    @Test
    fun resolveHeaderDisplayName_prefersAuthorDisplayNameWhenStoredNameMatchesHandle() {
        assertEquals(
            "Alice Doe",
            resolveHeaderDisplayName(
                primaryName = "@alice",
                sourceHandle = "alice",
                authorDisplayNames = listOf("Alice Doe"),
            ),
        )
    }

    @Test
    fun resolveHeaderDisplayName_returnsNullWhenPrimaryAlreadyLooksLikeDisplayName() {
        assertNull(
            resolveHeaderDisplayName(
                primaryName = "Alice Doe",
                sourceHandle = "alice",
                authorDisplayNames = listOf("Alice Doe"),
            ),
        )
    }
}

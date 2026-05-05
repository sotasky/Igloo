package com.screwy.igloo.moments

internal fun momentDisplayText(description: String?, title: String?): String =
    description?.takeIf { it.isNotBlank() }
        ?: title?.takeIf { it.isNotBlank() }
        ?: ""

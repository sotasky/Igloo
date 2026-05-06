package com.screwy.igloo.testutil

import androidx.lifecycle.ViewModel

class ViewModelTestTracker {
    private val viewModels = mutableListOf<ViewModel>()

    fun <T : ViewModel> track(viewModel: T): T =
        viewModel.also { viewModels += it }

    fun clearAll() {
        viewModels.asReversed().forEach(::clearViewModel)
        viewModels.clear()
    }
}

fun clearViewModel(viewModel: ViewModel) {
    val clearMethod = listOf(
        "clear\$lifecycle_viewmodel",
        "clear\$lifecycle_viewmodel_release",
    ).firstNotNullOf { methodName ->
        runCatching { ViewModel::class.java.getDeclaredMethod(methodName) }.getOrNull()
    }
    clearMethod
        .apply { isAccessible = true }
        .invoke(viewModel)
}

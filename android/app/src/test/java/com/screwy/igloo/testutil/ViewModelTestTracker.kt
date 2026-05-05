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
    ViewModel::class.java
        .getDeclaredMethod("clear\$lifecycle_viewmodel_release")
        .apply { isAccessible = true }
        .invoke(viewModel)
}

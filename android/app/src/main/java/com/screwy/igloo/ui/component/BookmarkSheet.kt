package com.screwy.igloo.ui.component

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.launch
import kotlinx.coroutines.yield
import kotlinx.serialization.json.Json
import org.koin.compose.koinInject

/** Target item being bookmarked. */
data class BookmarkTarget(
    val itemId: String,
    val authorHandle: String,
    val mediaCount: Int,
    val currentBookmark: BookmarkState? = null,
    /**
     * Placeholder/default label hint for new bookmarks. The text field itself stays
     * empty to match the cross-client bookmark flow.
     */
    val defaultTitle: String? = null,
    /** Caller's smart pre-selection for media indices when bookmarking new; null → full range. */
    val defaultMediaIndices: List<Int>? = null,
    val sourceHandle: String? = null,
    val quoteAuthorHandle: String? = null,
    val bodyText: String? = null,
    val isRetweet: Boolean = false,
)

/** Existing bookmark state when editing — preselects category + media/account indices. */
data class BookmarkState(
    val categoryId: Long,
    val customTitle: String?,
    val mediaIndices: List<Int>?,
    val accountHandles: List<String>? = null,
)

/** Row shape for the category picker list. */
data class BookmarkCategoryDisplay(
    val categoryId: Long,
    val name: String,
) {
    val isPending: Boolean get() = categoryId < 0L
}

/** Payload emitted by Confirm. `mediaIndices == null` signals "all indices." */
data class BookmarkPayload(
    val categoryId: Long,
    val customTitle: String?,
    val mediaIndices: List<Int>?,
    val accountHandles: List<String>? = null,
)

/**
 * Top-attached bookmark surface that lets the user pick a category, set a custom title,
 * choose account pills, and (for multi-media items) pick which media indices to bookmark.
 *
 * Bookmark behavior: empty label field, remembered category/account defaults, label
 * suggestions, and immediate label entry.
 */
@OptIn(
    ExperimentalMaterial3Api::class,
    ExperimentalLayoutApi::class,
    ExperimentalComposeUiApi::class,
)
@Composable
fun BookmarkSheet(
    target: BookmarkTarget,
    categories: List<BookmarkCategoryDisplay>,
    onConfirm: (BookmarkPayload) -> Unit,
    onRemove: (() -> Unit)? = null,
    onDismiss: () -> Unit,
    onCreateCategory: (name: String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val chipColors = FilterChipDefaults.filterChipColors(
        containerColor = colors.surfaceElevated,
        labelColor = colors.onSurface,
        selectedContainerColor = colors.primary,
        selectedLabelColor = colors.onPrimary,
    )
    val isEditing = target.currentBookmark != null
    val prefs: PreferencesRepo = koinInject()
    val dbHolder: DatabaseHolder = koinInject()
    val db = remember(dbHolder) { dbHolder.requireCurrent() }
    val focusRequester = remember { FocusRequester() }
    val keyboardController = LocalSoftwareKeyboardController.current
    val createCategoryBringIntoView = remember { BringIntoViewRequester() }
    val scope = rememberCoroutineScope()

    val labelSuggestions by db.bookmarkLabelDao().labelSuggestionsFlow().collectAsState(initial = emptyList())
    val bookmarkedHandleSet by produceState(initialValue = emptySet<String>(), key1 = target.itemId) {
        value = runCatching {
            loadBookmarkedHandleSet(db.bookmarkDao().accountHandleSelections())
        }.getOrDefault(emptySet())
    }
    val rememberedCategoryId by produceState<Long?>(initialValue = null, key1 = target.itemId) {
        value = prefs.getLastBookmarkCategoryId()
    }
    val rememberedAccountHandles by produceState(initialValue = emptyList<String>(), key1 = target.itemId) {
        val channelKey = bookmarkChannelKey(target)
        value = if (channelKey != null) {
            prefs.getBookmarkAccountPrefs(channelKey).orEmpty()
        } else {
            emptyList()
        }
    }

    val accountHandles = remember(target) { buildBookmarkAccountOptions(target) }
    val initialSelectedHandles = remember(target, rememberedAccountHandles, bookmarkedHandleSet) {
        initialSelectedAccountHandles(
            target = target,
            accountHandles = accountHandles,
            rememberedAccountHandles = rememberedAccountHandles,
            bookmarkedHandleSet = bookmarkedHandleSet,
        )
    }

    var selectedCategoryId: Long? by remember(target, rememberedCategoryId) {
        mutableStateOf(
            target.currentBookmark?.categoryId ?: rememberedCategoryId,
        )
    }
    var customTitle by remember(target) {
        mutableStateOf(bookmarkLabelTextFieldValue(target.currentBookmark?.customTitle))
    }
    var selectedAccountHandles by remember(target, rememberedAccountHandles, bookmarkedHandleSet) {
        mutableStateOf(initialSelectedHandles)
    }
    var selectedIndices by remember(target) {
        mutableStateOf(
            defaultMediaIndices(
                mediaCount = target.mediaCount,
                existing = target.currentBookmark?.mediaIndices,
                smartDefault = target.defaultMediaIndices,
            ),
        )
    }
    var showCreateInput by remember(target) { mutableStateOf(false) }
    var newCategoryName by remember(target) { mutableStateOf("") }
    var pendingCreatedCategoryName by remember(target) { mutableStateOf<String?>(null) }
    val selectedCategory = categories.firstOrNull { it.categoryId == selectedCategoryId }
    val waitingForCategorySync = selectedCategory?.isPending == true
    val filteredSuggestions = remember(customTitle.text, labelSuggestions) {
        filterLabelSuggestions(customTitle.text, labelSuggestions)
    }
    val labelPlaceholder = bookmarkLabelPlaceholder(
        defaultTitle = target.defaultTitle,
        fallback = stringResource(R.string.bookmark_label_optional),
    )

    fun createCategoryFromInput() {
        val name = newCategoryName.trim()
        when {
            name.isEmpty() || pendingCreatedCategoryName != null -> Unit
            else -> {
                val existing = findCategoryByName(categories, name)
                if (existing != null) {
                    selectedCategoryId = existing.categoryId
                    pendingCreatedCategoryName = if (existing.isPending) existing.name else null
                    showCreateInput = false
                    newCategoryName = ""
                } else {
                    pendingCreatedCategoryName = name
                    selectedCategoryId = null
                    onCreateCategory(name)
                }
            }
        }
    }

    fun submitBookmark() {
        val catId = selectedCategoryId ?: return
        if (waitingForCategorySync) return
        val payload = buildPayload(
            target = target,
            categoryId = catId,
            customTitle = customTitle.text,
            mediaIndices = selectedIndices,
            mediaCount = target.mediaCount,
            selectedAccountHandles = selectedAccountHandles,
            availableAccountHandles = accountHandles,
        )
        scope.launch {
            prefs.setLastBookmarkCategoryId(catId)
            bookmarkChannelKey(target)?.takeIf { it.isNotBlank() }?.let { key ->
                prefs.setBookmarkAccountPrefs(key, payload.accountHandles.orEmpty())
            }
        }
        onConfirm(payload)
    }

    LaunchedEffect(target.itemId) {
        yield()
        focusRequester.requestFocus()
        keyboardController?.show()
    }

    LaunchedEffect(categories, pendingCreatedCategoryName, target.currentBookmark, rememberedCategoryId) {
        val createdMatch = pendingCreatedCategoryName?.let { findCategoryByName(categories, it) }
        if (createdMatch != null) {
            selectedCategoryId = createdMatch.categoryId
            showCreateInput = false
            newCategoryName = ""
            if (!createdMatch.isPending) {
                pendingCreatedCategoryName = null
            }
        } else {
            val resolved = resolveCategorySelection(
                selectedCategoryId = selectedCategoryId
                    ?: target.currentBookmark?.categoryId
                    ?: rememberedCategoryId,
                categories = categories,
            )
            if (resolved != selectedCategoryId) {
                selectedCategoryId = resolved
            }
        }
    }

    val backdropInteraction = remember { MutableInteractionSource() }
    val panelInteraction = remember { MutableInteractionSource() }

    BackHandler(onBack = onDismiss)

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(colors.overlayDim.copy(alpha = 0.32f))
            .clickable(
                interactionSource = backdropInteraction,
                indication = null,
                onClick = onDismiss,
            )
            .padding(horizontal = 10.dp),
    ) {
        Surface(
            color = colors.surface,
            shape = RoundedCornerShape(16.dp),
            tonalElevation = 8.dp,
            shadowElevation = 12.dp,
            modifier = Modifier
                .align(Alignment.TopCenter)
                .statusBarsPadding()
                .padding(top = 8.dp)
                .widthIn(max = 520.dp)
                .fillMaxWidth()
                .heightIn(max = 620.dp)
                .clickable(
                    interactionSource = panelInteraction,
                    indication = null,
                    onClick = {},
                ),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = 14.dp, vertical = 12.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = stringResource(
                        if (isEditing) R.string.action_manage_bookmark else R.string.action_bookmark,
                    ),
                    style = MaterialTheme.typography.titleSmall,
                    color = colors.onSurface,
                )

                Text(
                    text = stringResource(R.string.bookmark_category),
                    style = MaterialTheme.typography.labelMedium,
                    color = colors.onSurfaceMuted,
                )
            // Category pills + inline "+" chip. Keeping the add action as a real
            // FilterChip makes it line up with the category chips naturally.
            FlowRow(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                categories.forEach { cat ->
                    FilterChip(
                        selected = selectedCategoryId == cat.categoryId,
                        onClick = { selectedCategoryId = cat.categoryId },
                        colors = chipColors,
                        label = {
                            Text(
                                if (cat.isPending) {
                                    stringResource(R.string.bookmark_category_creating, cat.name)
                                } else {
                                    cat.name
                                },
                            )
                        },
                    )
                }
                FilterChip(
                    selected = false,
                    onClick = {
                        showCreateInput = true
                        scope.launch {
                            yield()
                            runCatching { createCategoryBringIntoView.bringIntoView() }
                        }
                    },
                    colors = chipColors,
                    label = { Text("+") },
                )
            }

            if (showCreateInput) {
                val isCreatingCategory = pendingCreatedCategoryName != null
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .bringIntoViewRequester(createCategoryBringIntoView),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    OutlinedTextField(
                        value = newCategoryName,
                        onValueChange = { newCategoryName = it },
                        label = { Text(stringResource(R.string.bookmark_new_category)) },
                        singleLine = true,
                        enabled = !isCreatingCategory,
                        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                        keyboardActions = KeyboardActions(onDone = { createCategoryFromInput() }),
                        modifier = Modifier.weight(1f),
                    )
                    Button(
                        onClick = { createCategoryFromInput() },
                        enabled = newCategoryName.trim().isNotEmpty() && !isCreatingCategory,
                    ) {
                        Text(
                            if (isCreatingCategory) {
                                stringResource(R.string.bookmark_creating)
                            } else {
                                stringResource(R.string.bookmark_add_category)
                            },
                        )
                    }
                }
            }

            if (waitingForCategorySync) {
                Text(
                    text = stringResource(R.string.bookmark_waiting_for_category_sync_before_saving),
                    style = MaterialTheme.typography.bodySmall,
                    color = colors.onSurfaceMuted,
                )
            }

            if (accountHandles.isNotEmpty()) {
                Text(
                    text = stringResource(R.string.bookmark_account),
                    style = MaterialTheme.typography.labelMedium,
                    color = colors.onSurfaceMuted,
                )
                FlowRow(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    accountHandles.forEach { handle ->
                        FilterChip(
                            selected = handle in selectedAccountHandles,
                            onClick = {
                                selectedAccountHandles = if (handle in selectedAccountHandles) {
                                    selectedAccountHandles - handle
                                } else {
                                    selectedAccountHandles + handle
                                }
                            },
                            colors = chipColors,
                            label = { Text(handle) },
                        )
                    }
                }
            }

            Text(
                text = stringResource(R.string.bookmark_label_optional),
                style = MaterialTheme.typography.labelMedium,
                color = colors.onSurfaceMuted,
            )
            OutlinedTextField(
                value = customTitle,
                onValueChange = { customTitle = it },
                placeholder = { Text(labelPlaceholder) },
                minLines = 1,
                maxLines = 4,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { submitBookmark() }),
                modifier = Modifier
                    .fillMaxWidth()
                    .focusRequester(focusRequester),
            )
            if (filteredSuggestions.isNotEmpty() && customTitle.text.isNotBlank()) {
                Surface(
                    tonalElevation = 2.dp,
                    shape = RoundedCornerShape(12.dp),
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    LazyColumn(modifier = Modifier.heightIn(max = 180.dp)) {
                        items(filteredSuggestions) { suggestion ->
                            Text(
                                text = suggestion,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .clickable { customTitle = bookmarkLabelTextFieldValue(suggestion) }
                                    .padding(horizontal = 12.dp, vertical = 10.dp),
                                color = colors.onSurface,
                                style = MaterialTheme.typography.bodyMedium,
                            )
                        }
                    }
                }
            }

            if (target.mediaCount > 1) {
                Text(
                    text = stringResource(
                        R.string.bookmark_pick_media_to_download_count,
                        selectedIndices.size,
                        target.mediaCount,
                    ),
                    style = MaterialTheme.typography.labelSmall,
                    color = colors.onSurfaceMuted,
                )
                // Numeric badges are compact 32dp squares with labelSmall text,
                // 1-indexed, and filled primary when selected.
                FlowRow(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(5.dp),
                    verticalArrangement = Arrangement.spacedBy(5.dp),
                ) {
                    for (idx in 0 until target.mediaCount) {
                        val selected = idx in selectedIndices
                        Surface(
                            shape = RoundedCornerShape(6.dp),
                            color = if (selected) colors.primary else colors.surface,
                            modifier = Modifier
                                .size(32.dp)
                                .border(
                                    width = 1.dp,
                                    color = if (selected) colors.primary else colors.borderSubtle,
                                    shape = RoundedCornerShape(6.dp),
                                )
                                .clickable {
                                    selectedIndices = if (selected) {
                                        selectedIndices - idx
                                    } else {
                                        (selectedIndices + idx).sorted()
                                    }
                                },
                        ) {
                            Box(
                                contentAlignment = Alignment.Center,
                                modifier = Modifier.size(32.dp),
                            ) {
                                Text(
                                    text = "${idx + 1}",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = if (selected) colors.onPrimary else colors.onSurface,
                                )
                            }
                        }
                    }
                }
            }

            Spacer(modifier = Modifier.height(4.dp))

            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (isEditing && onRemove != null) {
                    TextButton(onClick = onRemove) { Text(stringResource(R.string.action_remove)) }
                    Spacer(modifier = Modifier.weight(1f))
                }
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) }
                Spacer(modifier = Modifier.size(8.dp))
                Button(
                    onClick = { submitBookmark() },
                    enabled = selectedCategoryId != null && !waitingForCategorySync,
                ) {
                    Text(stringResource(R.string.action_save))
                }
            }
        }
        }
    }
}

/**
 * Default media-index selection for a new or editing bookmark.
 *  - `existing != null` → echoed (editing flow wins over any smart default).
 *  - else if `smartDefault != null` → caller-supplied pre-selection (e.g. single parent video).
 *  - else → full range of [0, mediaCount).
 */
internal fun defaultMediaIndices(
    mediaCount: Int,
    existing: List<Int>?,
    smartDefault: List<Int>? = null,
): List<Int> = existing ?: smartDefault ?: (0 until mediaCount).toList()

internal fun bookmarkLabelTextFieldValue(text: String?): TextFieldValue {
    val value = text.orEmpty()
    return TextFieldValue(
        text = value,
        selection = TextRange(value.length),
    )
}

internal fun bookmarkLabelPlaceholder(defaultTitle: String?, fallback: String): String = fallback

internal fun initialCategorySelection(
    target: BookmarkTarget,
    categories: List<BookmarkCategoryDisplay>,
    rememberedCategoryId: Long? = null,
): Long? = resolveCategorySelection(
    selectedCategoryId = target.currentBookmark?.categoryId ?: rememberedCategoryId,
    categories = categories,
)

internal fun resolveCategorySelection(
    selectedCategoryId: Long?,
    categories: List<BookmarkCategoryDisplay>,
): Long? {
    if (categories.isEmpty()) return null
    if (selectedCategoryId != null && categories.any { it.categoryId == selectedCategoryId }) {
        return selectedCategoryId
    }
    return categories.firstOrNull { !it.isPending }?.categoryId ?: categories.first().categoryId
}

internal fun findCategoryByName(
    categories: List<BookmarkCategoryDisplay>,
    name: String,
): BookmarkCategoryDisplay? {
    val trimmed = name.trim()
    if (trimmed.isEmpty()) return null
    return categories.lastOrNull { it.name.equals(trimmed, ignoreCase = true) }
}

internal fun parseStoredHandles(raw: String?): List<String>? {
    val trimmed = raw?.trim().orEmpty()
    if (trimmed.isEmpty()) return null
    val parsed = if (trimmed.startsWith("[")) {
        runCatching { Json.decodeFromString<List<String>>(trimmed) }.getOrElse { emptyList() }
    } else {
        trimmed.split(',')
    }
    return parsed
        .map(::normalizeHandle)
        .filter(String::isNotBlank)
        .distinct()
        .ifEmpty { null }
}

internal fun parseStoredMediaIndices(raw: String?): List<Int>? {
    val trimmed = raw?.trim().orEmpty()
    if (trimmed.isEmpty()) return null
    val parsed = if (trimmed.startsWith("[")) {
        runCatching { Json.decodeFromString<List<Int>>(trimmed) }.getOrElse { emptyList() }
    } else {
        trimmed.split(',').mapNotNull { it.trim().toIntOrNull() }
    }
    return parsed
        .distinct()
        .sorted()
        .ifEmpty { null }
}

internal fun BookmarkEntity.toBookmarkState(): BookmarkState =
    BookmarkState(
        categoryId = categoryId,
        customTitle = customTitle,
        mediaIndices = parseStoredMediaIndices(mediaIndices),
        accountHandles = parseStoredHandles(accountHandles),
    )

internal fun loadBookmarkedHandleSet(selections: List<String>): Set<String> =
    selections
        .flatMap { parseStoredHandles(it).orEmpty() }
        .map(String::lowercase)
        .toSet()

internal fun bookmarkChannelKey(target: BookmarkTarget): String? =
    normalizeHandle(target.sourceHandle.takeIf { !it.isNullOrBlank() } ?: target.authorHandle)
        .takeIf { it.isNotBlank() }

internal fun buildBookmarkAccountOptions(target: BookmarkTarget): List<String> {
    val ordered = linkedSetOf<String>()
    target.currentBookmark?.accountHandles.orEmpty().forEach { handle ->
        normalizeHandle(handle).takeIf { it.isNotBlank() }?.let(ordered::add)
    }
    normalizeHandle(target.authorHandle).takeIf { it.isNotBlank() }?.let(ordered::add)
    val normalizedSource = normalizeHandle(target.sourceHandle)
    if (target.isRetweet && normalizedSource.isNotBlank() &&
        !normalizedSource.equals(normalizeHandle(target.authorHandle), ignoreCase = true)
    ) {
        ordered += normalizedSource
    }
    normalizeHandle(target.quoteAuthorHandle).takeIf { it.isNotBlank() }?.let(ordered::add)
    BOOKMARK_SHEET_MENTION_REGEX.findAll(target.bodyText.orEmpty())
        .map { normalizeHandle(it.groupValues.getOrNull(1)) }
        .filter(String::isNotBlank)
        .forEach(ordered::add)
    return ordered.toList()
}

internal fun initialSelectedAccountHandles(
    target: BookmarkTarget,
    accountHandles: List<String>,
    rememberedAccountHandles: List<String>,
    bookmarkedHandleSet: Set<String>,
): Set<String> {
    val current = target.currentBookmark?.accountHandles.orEmpty()
        .map(::normalizeHandle)
        .filter(String::isNotBlank)
        .toSet()
    if (current.isNotEmpty()) return current

    val remembered = rememberedAccountHandles
        .map(::normalizeHandle)
        .filter(String::isNotBlank)
        .toSet()
        .intersect(accountHandles.toSet())
    if (remembered.isNotEmpty()) return remembered

    val bookmarked = accountHandles.firstOrNull { it in bookmarkedHandleSet }
    if (bookmarked != null) return setOf(bookmarked)

    return accountHandles.firstOrNull()?.let(::setOf).orEmpty()
}

internal fun filterLabelSuggestions(
    query: String,
    labels: List<String>,
): List<String> {
    val normalized = query.trim().lowercase()
    if (normalized.isEmpty()) return emptyList()
    return labels
        .filter {
            val label = it.trim()
            label.isNotEmpty() &&
                label.lowercase() != normalized &&
                label.lowercase().contains(normalized)
        }
        .sortedWith(
            compareBy<String> { !it.lowercase().startsWith(normalized) }
                .thenBy { it.lowercase() },
        )
        .take(6)
}

/**
 * Normalizes user input into a [BookmarkPayload]:
 *  - blank/whitespace `customTitle` → null
 *  - `mediaIndices == full range [0, mediaCount)` → null (signals "all")
 *  - account handles come from the selected account pills; if the sheet had no
 *    account section at all, fall back to the primary author handle.
 */
internal fun buildPayload(
    target: BookmarkTarget,
    categoryId: Long,
    customTitle: String,
    mediaIndices: List<Int>,
    mediaCount: Int,
    selectedAccountHandles: Set<String>,
    availableAccountHandles: List<String>,
): BookmarkPayload {
    val trimmedTitle: String? = customTitle.trim().ifEmpty { null }
    val fullRange: List<Int> = (0 until mediaCount).toList()
    val normalizedIndices: List<Int>? =
        if (mediaIndices.sorted() == fullRange) null else mediaIndices.sorted()
    val normalizedAccountHandles = selectedAccountHandles
        .map(::normalizeHandle)
        .filter(String::isNotBlank)
        .distinct()
        .sorted()
        .ifEmpty {
            if (availableAccountHandles.isEmpty()) {
                normalizeHandle(target.authorHandle).takeIf { it.isNotBlank() }?.let(::listOf)
            } else {
                null
            }
        }
    return BookmarkPayload(
        categoryId = categoryId,
        customTitle = trimmedTitle,
        accountHandles = normalizedAccountHandles,
        mediaIndices = normalizedIndices,
    )
}

private val BOOKMARK_SHEET_MENTION_REGEX = Regex("@([A-Za-z0-9_]{4,})")

package com.screwy.igloo.settings

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.DpOffset
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.screwy.igloo.R
import com.screwy.igloo.ui.theme.CatppuccinAccentChoice
import com.screwy.igloo.ui.theme.allThemeSpecs
import com.screwy.igloo.ui.theme.iglooColors
import com.screwy.igloo.ui.theme.normalizeHex
import com.screwy.igloo.ui.theme.themeColor
import org.koin.androidx.compose.koinViewModel

internal val ThemeDropdownItemHeight = 56.dp
private val ThemeDropdownMenuMaxHeight = 360.dp

internal fun themeDropdownMenuYOffset(selectedIndex: Int) =
    if (selectedIndex <= 0) 0.dp else ThemeDropdownItemHeight * -selectedIndex.toFloat()

/** Theme picker route. Writes are reactive through `MainActivity`'s `IglooTheme`. */
@Composable
fun ThemeRoute(
    modifier: Modifier = Modifier,
) {
    val vm: ThemeViewModel = koinViewModel()
    val themeId by vm.themeId.collectAsStateWithLifecycle()
    val accentHex by vm.accentHex.collectAsStateWithLifecycle()
    val catppuccinAccents by vm.catppuccinAccents.collectAsStateWithLifecycle()
    val customCss by vm.customCss.collectAsStateWithLifecycle()

    Column(modifier = modifier.fillMaxWidth().padding(16.dp)) {
        ThemePickerSection(current = themeId, onSelect = vm::setThemeId)
        Spacer(Modifier.height(24.dp))
        if (catppuccinAccents.isNotEmpty()) {
            CatppuccinAccentSection(
                choices = catppuccinAccents,
                currentHex = accentHex,
                onSelect = vm::setAccentHex,
            )
            Spacer(Modifier.height(24.dp))
        }
        AccentHexSection(currentHex = accentHex, onValidAccent = vm::setAccentHex)
        Spacer(Modifier.height(24.dp))
        CustomCssSection(css = customCss, onChange = vm::setCustomCss)
    }
}

@Composable
private fun ThemePickerSection(
    current: String,
    onSelect: (String) -> Unit,
) {
    val label = stringResource(R.string.label_theme)
    val colors = MaterialTheme.iglooColors
    val themes = remember { allThemeSpecs() }
    val selected = themes.firstOrNull { it.id == current } ?: themes.first()
    val selectedIndex = themes.indexOfFirst { it.id == selected.id }.coerceAtLeast(0)
    val density = LocalDensity.current
    var anchorWidthPx by remember { mutableStateOf(0) }
    val anchorWidth = with(density) { anchorWidthPx.toDp() }
    val menuWidthModifier = if (anchorWidthPx > 0) Modifier.width(anchorWidth) else Modifier
    var expanded by remember { mutableStateOf(false) }
    Column {
        SectionTitle(label)
        Box {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(10.dp))
                    .background(colors.surfaceElevated)
                    .border(1.dp, colors.borderSubtle, RoundedCornerShape(10.dp))
                    .clickable { expanded = true }
                    .onSizeChanged { anchorWidthPx = it.width }
                    .padding(horizontal = 14.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Box(
                    modifier = Modifier
                        .size(14.dp)
                        .clip(CircleShape)
                        .background(themeColor(selected.defaultAccent) ?: colors.primary),
                )
                Text(
                    text = selected.label,
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                Icon(
                    imageVector = Icons.Default.KeyboardArrowDown,
                    contentDescription = null,
                    tint = colors.onSurfaceMuted,
                )
            }
            DropdownMenu(
                expanded = expanded,
                onDismissRequest = { expanded = false },
                offset = DpOffset(x = 0.dp, y = themeDropdownMenuYOffset(selectedIndex)),
                modifier = menuWidthModifier
                    .heightIn(max = ThemeDropdownMenuMaxHeight),
            ) {
                themes.forEach { theme ->
                    DropdownMenuItem(
                        modifier = Modifier.height(ThemeDropdownItemHeight),
                        text = {
                            Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(10.dp),
                            ) {
                                Box(
                                    modifier = Modifier
                                        .size(14.dp)
                                        .clip(CircleShape)
                                        .background(themeColor(theme.defaultAccent) ?: colors.primary),
                                )
                                Text(
                                    text = theme.label,
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = colors.onSurface,
                                )
                            }
                        },
                        onClick = {
                            onSelect(theme.id)
                            expanded = false
                        },
                    )
                }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun CatppuccinAccentSection(
    choices: List<CatppuccinAccentChoice>,
    currentHex: String,
    onSelect: (String) -> Unit,
) {
    val label = stringResource(R.string.settings_theme_catppuccin_accents)
    Column {
        SectionTitle(label)
        FlowRow(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            choices.forEach { choice ->
                CatppuccinAccentPill(
                    choice = choice,
                    selected = normalizeHex(choice.hex) == normalizeHex(currentHex),
                    onClick = { onSelect(choice.hex) },
                )
            }
        }
    }
}

@Composable
private fun CatppuccinAccentPill(
    choice: CatppuccinAccentChoice,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val shape = RoundedCornerShape(999.dp)
    Row(
        modifier = Modifier
            .clip(shape)
            .background(if (selected) colors.primaryMuted else colors.surface)
            .border(1.dp, if (selected) colors.primary else colors.borderSubtle, shape)
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 7.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Box(
            modifier = Modifier
                .size(13.dp)
                .clip(CircleShape)
                .background(themeColor(choice.hex) ?: colors.primary),
        )
        Text(
            text = choice.label,
            style = MaterialTheme.typography.bodySmall,
            color = if (selected) colors.onSurface else colors.onSurfaceMuted,
            maxLines = 1,
        )
    }
}

@Composable
private fun AccentHexSection(
    currentHex: String,
    onValidAccent: (String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val label = stringResource(R.string.settings_theme_accent)
    val invalidLabel = stringResource(R.string.settings_theme_accent_invalid)
    var text by rememberSaveable { mutableStateOf(currentHex) }
    LaunchedEffect(currentHex) {
        if (normalizeHex(text) != normalizeHex(currentHex)) {
            text = currentHex
        }
    }
    val normalized = normalizeHex(text)
    val hasError = text.isNotBlank() && normalized == null

    Column {
        SectionTitle(label)
        Row(
            verticalAlignment = Alignment.Top,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Box(
                modifier = Modifier
                    .padding(top = 14.dp)
                    .size(32.dp)
                    .clip(CircleShape)
                    .background(themeColor(normalized ?: currentHex) ?: colors.primary)
                    .border(1.dp, colors.borderSubtle, CircleShape),
            )
            OutlinedTextField(
                value = text,
                onValueChange = { value ->
                    text = value
                    normalizeHex(value)?.let(onValidAccent)
                },
                singleLine = true,
                isError = hasError,
                supportingText = {
                    if (hasError) {
                        Text(invalidLabel)
                    }
                },
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
private fun CustomCssSection(
    css: String,
    onChange: (String) -> Unit,
) {
    val label = stringResource(R.string.settings_theme_custom_css)
    var text by rememberSaveable { mutableStateOf(css) }
    LaunchedEffect(css) {
        if (text != css) {
            text = css
        }
    }

    Column {
        SectionTitle(label)
        OutlinedTextField(
            value = text,
            onValueChange = { value ->
                text = value
                onChange(value)
            },
            minLines = 6,
            maxLines = 12,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.iglooColors.primary,
        modifier = Modifier.padding(vertical = 8.dp),
    )
}

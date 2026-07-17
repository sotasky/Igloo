package com.screwy.igloo.ui.component

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.outlined.StarBorder
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.res.stringResource
import coil3.compose.AsyncImage
import com.screwy.igloo.R
import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.entity.ChannelDisplay
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.theme.IglooColors
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.compose.koinInject

enum class Platform { Twitter, TikTok, YouTube, Instagram }

internal object ChannelProfileHeaderDefaults {
    const val CardHorizontalMarginDp = 8
    const val CardRadiusDp = 8
    const val CardHorizontalPaddingDp = 14
    const val CardVerticalPaddingDp = 10
    const val NameHandleSpacingDp = 2
    const val SectionSpacingDp = 8
    const val StatsSpacingDp = 18
    const val ComposeBannerHeightDp = 148
    const val ComposeAvatarSizeDp = 92
    const val ComposeAvatarOverlapDp = ComposeAvatarSizeDp / 2
    const val InlineAvatarSizeDp = 60
}

internal data class ChannelProfileHeaderLabels(
    val following: String,
    val followers: String,
    val subscribers: String,
    val protectedAccount: String,
    val browser: String,
)

internal enum class ChannelProfileHeaderLinkColorRole {
    Primary,
}

internal data class ChannelProfileHeaderUiModel(
    val channelId: String,
    val platform: Platform?,
    val displayName: String,
    val handle: String,
    val platformLabel: String,
    val openLabel: String,
    val platformUrl: String?,
    val bio: String = "",
    val website: String = "",
    val stats: List<String> = emptyList(),
    val protectedText: String,
    val isProtected: Boolean = false,
    val isVerified: Boolean = false,
    val verifiedType: String? = null,
    val isFollowed: Boolean = false,
    val isStarred: Boolean = false,
    val storyRingState: StoryRingState = StoryRingState.None,
    val storyFirstVideoId: String = "",
    val linkColorRole: ChannelProfileHeaderLinkColorRole = ChannelProfileHeaderLinkColorRole.Primary,
)

internal data class ChannelProfileOverflowControls(
    val canToggleReposts: Boolean = false,
    val repostsEnabled: Boolean = true,
    val canToggleMute: Boolean = false,
    val isMuted: Boolean = false,
)

/** Reposts belong to followed accounts; mute is available for other accounts. */
internal fun channelProfileOverflowControls(
    platform: Platform?,
    isFollowed: Boolean,
    repostsEnabled: Boolean,
    isMuted: Boolean,
): ChannelProfileOverflowControls {
    val supportsReposts = platform == Platform.TikTok || platform == Platform.Instagram
    return ChannelProfileOverflowControls(
        canToggleReposts = supportsReposts && isFollowed,
        repostsEnabled = repostsEnabled,
        // A previously muted account can always be unmuted, including after a follow.
        canToggleMute = supportsReposts && (!isFollowed || isMuted),
        isMuted = isMuted,
    )
}

@Composable
internal fun ComposeChannelHeader(
    header: ChannelProfileHeaderUiModel,
    onFollowToggle: (newValue: Boolean) -> Unit,
    onStarToggle: (newValue: Boolean) -> Unit,
    onRefresh: () -> Unit,
    onOpenInPlatform: () -> Unit,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit = { _, _ -> },
    onMentionClick: (handle: String) -> Unit,
    onOpenUrl: (url: String) -> Unit,
    overflowControls: ChannelProfileOverflowControls = ChannelProfileOverflowControls(),
    onRepostsEnabledChange: (Boolean) -> Unit = {},
    onMutedChange: (Boolean) -> Unit = {},
    modifier: Modifier = Modifier,
) {
    val colors: IglooColors = MaterialTheme.iglooColors
    val mediaResolvers: MediaResolvers = koinInject()
	val resolvedBannerUri by mediaResolvers.bannerForChannelFlow(header.channelId)
		.collectAsState(initial = MediaUri.Missing)
	val bannerUri = resolvedBannerUri
    val hasBanner = bannerUri !is MediaUri.Missing
    val bannerHeight = if (hasBanner) ChannelProfileHeaderDefaults.ComposeBannerHeightDp.dp else 0.dp
    val avatarSize = if (hasBanner) {
        ChannelProfileHeaderDefaults.ComposeAvatarSizeDp.dp
    } else {
        ChannelProfileHeaderDefaults.InlineAvatarSizeDp.dp
    }
    val avatarOverlap = if (hasBanner) ChannelProfileHeaderDefaults.ComposeAvatarOverlapDp.dp else 0.dp
    var menuOpen by remember { mutableStateOf(false) }
    val storyTarget = header.storyFirstVideoId.takeIf {
        it.isNotBlank() && header.storyRingState != StoryRingState.None
    }
    val profileAccountLabel = profileOverflowAccountLabel(header)
    val profileAccountMuteLabel = profileOverflowMuteLabel(header)

    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(colors.surface),
    ) {
        if (hasBanner) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(bannerHeight)
                    .background(
                        Brush.linearGradient(
                            colors = listOf(
                                colors.surfaceHighest,
                                colors.surfaceElevated,
                                colors.surface,
                            ),
                        ),
                    ),
            ) {
                if (bannerUri !is MediaUri.Missing) {
                    AsyncImage(
                        model = when (val uri = bannerUri) {
                            is MediaUri.Local -> uri.file
                            is MediaUri.Remote -> rememberRemoteImageModel(uri.url)
                            MediaUri.Missing -> null
                        },
                        contentDescription = stringResource(R.string.content_description_channel_banner),
                        contentScale = ContentScale.Crop,
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(bannerHeight)
                            .align(Alignment.TopCenter),
                    )
                }
                Avatar(
                    channelId = header.channelId,
                    size = avatarSize,
                    modifier = Modifier
                        .align(Alignment.BottomStart)
                        .padding(start = 16.dp)
                        .offset(y = avatarOverlap)
                        .storyRingBorder(header.storyRingState, colors)
                        .border(
                            width = 4.dp,
                            color = colors.surface,
                            shape = CircleShape,
                        ),
                    onClick = storyTarget?.let { firstVideoId ->
                        { onStoryClick(header.channelId, firstVideoId) }
                    },
                )
            }
        }

        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = ChannelProfileHeaderDefaults.CardHorizontalMarginDp.dp)
                .padding(top = if (hasBanner) 2.dp else 8.dp, bottom = 8.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            if (hasBanner) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .heightIn(min = avatarOverlap + 2.dp),
                    verticalAlignment = Alignment.Bottom,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Spacer(modifier = Modifier.width(avatarSize + 4.dp))
                    Spacer(modifier = Modifier.weight(1f))
                    HeaderActionRow(
                        isFollowed = header.isFollowed,
                        isStarred = header.isStarred,
                        colors = colors,
                        menuOpen = menuOpen,
                        onFollowToggle = onFollowToggle,
                        onStarToggle = onStarToggle,
                        onMenuOpenChange = { menuOpen = it },
                        onRefresh = onRefresh,
                        onOpenInPlatform = onOpenInPlatform,
                        canOpenInPlatform = !header.platformUrl.isNullOrBlank(),
                        openLabel = header.openLabel,
                        platform = header.platform,
                        accountLabel = profileAccountLabel,
                        accountMuteLabel = profileAccountMuteLabel,
                        overflowControls = overflowControls,
                        onRepostsEnabledChange = onRepostsEnabledChange,
                        onMutedChange = onMutedChange,
                    )
                }
            } else {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.Top,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Avatar(
                        channelId = header.channelId,
                        size = avatarSize,
                        modifier = Modifier
                            .storyRingBorder(header.storyRingState, colors),
                        onClick = storyTarget?.let { firstVideoId ->
                            { onStoryClick(header.channelId, firstVideoId) }
                        },
                    )
                    Spacer(modifier = Modifier.weight(1f))
                    HeaderActionRow(
                        isFollowed = header.isFollowed,
                        isStarred = header.isStarred,
                        colors = colors,
                        menuOpen = menuOpen,
                        onFollowToggle = onFollowToggle,
                        onStarToggle = onStarToggle,
                        onMenuOpenChange = { menuOpen = it },
                        onRefresh = onRefresh,
                        onOpenInPlatform = onOpenInPlatform,
                        canOpenInPlatform = !header.platformUrl.isNullOrBlank(),
                        openLabel = header.openLabel,
                        platform = header.platform,
                        accountLabel = profileAccountLabel,
                        accountMuteLabel = profileAccountMuteLabel,
                        overflowControls = overflowControls,
                        onRepostsEnabledChange = onRepostsEnabledChange,
                        onMutedChange = onMutedChange,
                    )
                }
            }

            ChannelProfileInfoCard(
                header = header,
                colors = colors,
                onMentionClick = onMentionClick,
                onOpenUrl = onOpenUrl,
            )
        }
    }
}

@Composable
private fun ChannelProfileInfoCard(
    header: ChannelProfileHeaderUiModel,
    colors: IglooColors,
    onMentionClick: (handle: String) -> Unit,
    onOpenUrl: (url: String) -> Unit,
) {
    val linkColor = colors.channelProfileHeaderLinkColor(header.linkColorRole)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(colors.surfaceElevated, RoundedCornerShape(ChannelProfileHeaderDefaults.CardRadiusDp.dp))
            .padding(
                horizontal = ChannelProfileHeaderDefaults.CardHorizontalPaddingDp.dp,
                vertical = ChannelProfileHeaderDefaults.CardVerticalPaddingDp.dp,
            ),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(
                text = header.displayName,
                style = MaterialTheme.typography.titleLarge.copy(fontWeight = FontWeight.Bold),
                color = colors.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            if (header.isVerified) {
                VerifiedBadge(
                    platform = header.platform,
                    verifiedType = header.verifiedType,
                    colors = colors,
                )
            }
        }

        if (header.handle.isNotBlank()) {
            Text(
                text = "@${header.handle}",
                style = MaterialTheme.typography.bodyMedium,
                color = colors.onSurfaceHandle,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = ChannelProfileHeaderDefaults.NameHandleSpacingDp.dp),
            )
        }

        if (header.isProtected) {
            Text(
                text = header.protectedText,
                style = MaterialTheme.typography.bodyMedium,
                color = colors.onSurfaceMuted,
                modifier = Modifier.padding(top = ChannelProfileHeaderDefaults.SectionSpacingDp.dp),
            )
        } else {
            if (header.bio.isNotBlank()) {
                AtMentionText(
                    text = header.bio,
                    onMentionClick = onMentionClick,
                    onUrlClick = onOpenUrl,
                    style = MaterialTheme.typography.bodyMedium,
                    mentionColorOverride = linkColor,
                    urlColorOverride = linkColor,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = ChannelProfileHeaderDefaults.SectionSpacingDp.dp),
                )
            }
            if (header.website.isNotBlank()) {
                Text(
                    text = header.website,
                    style = MaterialTheme.typography.bodyMedium,
                    color = linkColor,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier
                        .padding(top = ChannelProfileHeaderDefaults.SectionSpacingDp.dp)
                        .clickable { onOpenUrl(header.website) },
                )
            }
            if (header.stats.isNotEmpty()) {
                Row(
                    horizontalArrangement = Arrangement.spacedBy(ChannelProfileHeaderDefaults.StatsSpacingDp.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = ChannelProfileHeaderDefaults.SectionSpacingDp.dp),
                ) {
                    header.stats.forEach { stat ->
                        Text(
                            text = stat,
                            style = MaterialTheme.typography.bodyMedium,
                            color = colors.onSurfaceMuted,
                        )
                    }
                }
            }
        }
    }
}

private fun IglooColors.channelProfileHeaderLinkColor(role: ChannelProfileHeaderLinkColorRole): Color =
    when (role) {
        ChannelProfileHeaderLinkColorRole.Primary -> primary
    }

@Composable
private fun HeaderActionRow(
    isFollowed: Boolean,
    isStarred: Boolean,
    colors: IglooColors,
    menuOpen: Boolean,
    onFollowToggle: (newValue: Boolean) -> Unit,
    onStarToggle: (newValue: Boolean) -> Unit,
    onMenuOpenChange: (Boolean) -> Unit,
    onRefresh: () -> Unit,
    onOpenInPlatform: () -> Unit,
    canOpenInPlatform: Boolean,
    openLabel: String,
    platform: Platform?,
    accountLabel: String,
    accountMuteLabel: String,
    overflowControls: ChannelProfileOverflowControls,
    onRepostsEnabledChange: (Boolean) -> Unit,
    onMutedChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        if (!isFollowed) {
            Button(onClick = { onFollowToggle(true) }) {
                Text(stringResource(R.string.action_follow))
            }
        } else {
            OutlinedButton(
                onClick = { onFollowToggle(false) },
                border = BorderStroke(1.dp, colors.primary.copy(alpha = 0.55f)),
                colors = ButtonDefaults.outlinedButtonColors(contentColor = colors.onSurface),
            ) {
                Text(stringResource(R.string.action_following))
            }
        }
        IconButton(onClick = { onStarToggle(!isStarred) }) {
            Icon(
                imageVector = if (isStarred) Icons.Filled.Star else Icons.Outlined.StarBorder,
                contentDescription = if (isStarred) {
                    stringResource(R.string.action_unstar)
                } else {
                    stringResource(R.string.action_star)
                },
                tint = if (isStarred) colors.primary else colors.onSurfaceMuted,
            )
        }
        Box {
            IconButton(onClick = { onMenuOpenChange(true) }) {
                Icon(
                    imageVector = Icons.Default.MoreVert,
                    contentDescription = stringResource(R.string.action_more),
                    tint = colors.onSurfaceMuted,
                )
            }
            DropdownMenu(
                expanded = menuOpen,
                onDismissRequest = { onMenuOpenChange(false) },
            ) {
                DropdownMenuItem(
                    text = {
                        Text(
                            stringResource(
                                if (isFollowed) R.string.action_unfollow_account else R.string.action_follow_account,
                            ),
                        )
                    },
                    onClick = {
                        onMenuOpenChange(false)
                        onFollowToggle(!isFollowed)
                    },
                )
                if (overflowControls.canToggleReposts) {
                    DropdownMenuItem(
                        text = {
                            Text(
                                stringResource(
                                    profileRepostsToggleLabelRes(
                                        platform = platform,
                                        repostsEnabled = overflowControls.repostsEnabled,
                                    ),
                                    accountLabel,
                                ),
                            )
                        },
                        onClick = {
                            onMenuOpenChange(false)
                            onRepostsEnabledChange(!overflowControls.repostsEnabled)
                        },
                    )
                }
                if (overflowControls.canToggleMute) {
                    DropdownMenuItem(
                        text = {
                            Text(
                                stringResource(
                                    if (overflowControls.isMuted) {
                                        R.string.action_unmute_account_label
                                    } else {
                                        R.string.action_mute_account_label
                                    },
                                    accountMuteLabel,
                                ),
                            )
                        },
                        onClick = {
                            onMenuOpenChange(false)
                            onMutedChange(!overflowControls.isMuted)
                        },
                    )
                }
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.action_refresh_channel)) },
                    onClick = {
                        onMenuOpenChange(false)
                        onRefresh()
                    },
                )
                if (canOpenInPlatform) {
                    DropdownMenuItem(
                        text = {
                            Text(
                                stringResource(
                                    R.string.action_open_in,
                                    openLabel,
                                ),
                            )
                        },
                        onClick = {
                            onMenuOpenChange(false)
                            onOpenInPlatform()
                        },
                    )
                }
            }
        }
    }
}

internal fun profileOverflowAccountLabel(header: ChannelProfileHeaderUiModel): String =
    header.displayName.trim().takeIf { it.isNotBlank() }
        ?: header.handle.trim().takeIf { it.isNotBlank() }
        ?: header.channelId

internal fun profileOverflowMuteLabel(header: ChannelProfileHeaderUiModel): String {
    val handle = normalizeHandle(header.handle)
    return if (handle.isNotBlank()) "@$handle" else profileOverflowAccountLabel(header)
}

private fun profileRepostsToggleLabelRes(platform: Platform?, repostsEnabled: Boolean): Int =
    when (platform) {
        Platform.Twitter -> {
            if (repostsEnabled) {
                R.string.action_turn_off_retweets_for_account
            } else {
                R.string.action_turn_on_retweets_for_account
            }
        }
        else -> {
            if (repostsEnabled) {
                R.string.action_turn_off_reposts_for_account
            } else {
                R.string.action_turn_on_reposts_for_account
            }
        }
    }

@Composable
private fun VerifiedBadge(
    platform: Platform?,
    verifiedType: String?,
    colors: IglooColors,
) {
    Box(
        modifier = Modifier
            .size(20.dp)
            .clip(CircleShape)
            .background(verifiedBadgeColor(platform, verifiedType, colors)),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = Icons.Default.Check,
            contentDescription = stringResource(R.string.status_verified),
            tint = Color.White,
            modifier = Modifier.size(12.dp),
        )
    }
}

internal fun parsePlatform(raw: String?): Platform? {
    if (raw == null) return null
    return when (raw.trim().lowercase()) {
        "twitter", "x" -> Platform.Twitter
        "tiktok" -> Platform.TikTok
        "youtube" -> Platform.YouTube
        "instagram" -> Platform.Instagram
        else -> null
    }
}

private fun profileDisplayName(
    override: String?,
    profileName: String?,
    fallbackName: String?,
    handle: String,
    channelId: String,
): String {
    val candidates = listOf(override, profileName, fallbackName)
        .mapNotNull { it?.trim()?.takeIf(String::isNotBlank) }
    return candidates.firstOrNull() ?: handle.ifBlank { channelId }
}

internal fun channelProfileHeaderUiModel(
	channel: ChannelDisplay,
	profile: ChannelProfileEntity?,
	displayNameOverride: String? = null,
	labels: ChannelProfileHeaderLabels,
): ChannelProfileHeaderUiModel {
    val platform = parsePlatform(profile?.platform)
        ?: parsePlatform(channel.channel.platform)
        ?: inferPlatformFromChannelId(channel.channel.channelId)
    val platformKey = platform?.profileHandlePlatformKey() ?: channel.channel.platform
    val handle = sequenceOf(
        profile?.handle,
        channel.channel.sourceId,
    )
        .map { platformHandleCandidate(platformKey, it) }
        .firstOrNull { it.isNotBlank() }
        .orEmpty()
    val displayName = profileDisplayName(
        override = displayNameOverride,
        profileName = profile?.displayName,
        fallbackName = channel.channel.name,
        handle = handle,
        channelId = channel.channel.channelId,
    )
    val platformUrl = channel.channel.url?.trim()?.takeIf(String::isNotBlank)
    val followersLabel = if (platform == Platform.YouTube) labels.subscribers else labels.followers
    val stats = buildList {
        val following = profile?.following ?: 0
        val followers = profile?.followers ?: 0
        if (following > 0) add("${compactCount(following.toLong())} ${labels.following}")
        if (followers > 0) add("${compactCount(followers.toLong())} $followersLabel")
    }
    return ChannelProfileHeaderUiModel(
        channelId = channel.channel.channelId,
        platform = platform,
        displayName = displayName,
        handle = handle,
        platformLabel = platform.profileHeaderLabel(labels),
        openLabel = platform.profileHeaderLabel(labels),
        platformUrl = platformUrl,
        bio = profile?.bio?.trim().orEmpty(),
        website = profile?.website?.trim().orEmpty(),
        stats = stats,
        protectedText = labels.protectedAccount,
        isProtected = profile?.isProtected == true,
        isVerified = profile?.verified == true,
        verifiedType = profile?.verifiedType,
        isFollowed = channel.isFollowed == 1,
        isStarred = channel.isStarred == 1,
    )
}

private fun Platform.profileHandlePlatformKey(): String =
    when (this) {
        Platform.Twitter -> "twitter"
        Platform.TikTok -> "tiktok"
        Platform.YouTube -> "youtube"
        Platform.Instagram -> "instagram"
    }

private fun inferPlatformFromChannelId(channelId: String): Platform? =
    parsePlatform(platformKeyFromChannelId(channelId))

private fun verifiedBadgeColor(platform: Platform?, verifiedType: String?, colors: IglooColors): Color {
    if (platform != Platform.Twitter) return colors.info
    return when (verifiedType?.trim()?.lowercase()) {
        "business" -> Color(0xFFC79A2E)
        "government" -> Color(0xFF8892A0)
        else -> colors.platformTwitter
    }
}

private fun Platform?.profileHeaderLabel(labels: ChannelProfileHeaderLabels): String = when (this) {
    Platform.Twitter -> "X"
    Platform.TikTok -> "TikTok"
    Platform.YouTube -> "YouTube"
    Platform.Instagram -> "Instagram"
    null -> labels.browser
}

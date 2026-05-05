package com.screwy.igloo.ui.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.screwy.igloo.R
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.theme.iglooColors

/**
 * Inline in-feed slideshow pager
 * "SlideshowCarousel". Stateless — parent supplies the resolved [MediaUri] per page.
 */
@Composable
fun SlideshowCarousel(
    mediaUris: List<MediaUri>,
    onItemClick: (index: Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (mediaUris.isEmpty()) return

    val colors = MaterialTheme.iglooColors
    val missingMediaLabel = stringResource(R.string.content_description_missing_media)
    val pagerState = rememberPagerState(pageCount = { mediaUris.size })

    Box(modifier = modifier.fillMaxWidth()) {
        HorizontalPager(
            state = pagerState,
            modifier = Modifier
                .fillMaxWidth()
                .height(260.dp),
        ) { page ->
            val uri = mediaUris[page]
            val backgroundColor = if (uri is MediaUri.Missing) colors.surfaceVariant else colors.surface
            val showBadge = isIglooRemoteOffline(uri)

            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .clip(RoundedCornerShape(8.dp))
                    .background(backgroundColor)
                    .clickable { onItemClick(page) }
                    .alpha(mediaAlpha(uri)),
                contentAlignment = Alignment.Center,
            ) {
                when (uri) {
                    is MediaUri.Local -> AsyncImage(
                        model = uri.file,
                        contentDescription = stringResource(R.string.content_description_slideshow_image_number, page + 1),
                        modifier = Modifier.fillMaxSize(),
                    )
                    is MediaUri.Remote -> AsyncImage(
                        model = rememberRemoteImageModel(uri.url),
                        contentDescription = stringResource(R.string.content_description_slideshow_image_number, page + 1),
                        modifier = Modifier.fillMaxSize(),
                    )
                    is MediaUri.Missing -> Icon(
                        imageVector = Icons.Default.BrokenImage,
                        contentDescription = missingMediaLabel,
                        tint = colors.onSurfaceFaint,
                        modifier = Modifier.size(32.dp),
                    )
                }
                if (showBadge) DownloadPendingBadge()
            }
        }

        if (mediaUris.size > 1) {
            Row(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(bottom = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                for (i in mediaUris.indices) {
                    val isActive = pagerState.currentPage == i
                    Box(
                        modifier = Modifier
                            .size(if (isActive) 8.dp else 6.dp)
                            .clip(CircleShape)
                            .background(if (isActive) colors.primary else colors.onSurfaceFaint),
                    )
                }
            }
        }
    }
}

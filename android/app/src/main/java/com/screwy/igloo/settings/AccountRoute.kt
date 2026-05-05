package com.screwy.igloo.settings

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.settings.components.SectionHeader
import com.screwy.igloo.settings.components.SettingsSubScreen
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.androidx.compose.koinViewModel

@Composable
fun AccountRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: AccountSettingsViewModel = koinViewModel()
    val serverUrl by vm.serverUrl.collectAsStateWithLifecycle()

    SettingsSubScreen(
        title = stringResource(R.string.settings_tab_account),
        onBack = { navController.popBackStack() },
        modifier = modifier,
    ) {
        SectionHeader(stringResource(R.string.settings_section_server))
        Text(
            text = serverUrl,
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.iglooColors.onSurface,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable { vm.logout() }
                .padding(horizontal = 16.dp, vertical = 14.dp),
        ) {
            Text(
                text = stringResource(R.string.action_logout),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.iglooColors.error,
            )
        }
    }
}

package com.nocap.link.ui

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.window.DialogProperties

/**
 * Every dialog in the app goes through here, so they all get the NoCapOS
 * look (frosted sheet, the web's 22 px corners, NoCap text colors) instead
 * of the stock Material one. Same parameters as Material's AlertDialog.
 */
@Composable
fun AlertDialog(
    onDismissRequest: () -> Unit,
    confirmButton: @Composable () -> Unit,
    modifier: Modifier = Modifier,
    dismissButton: (@Composable () -> Unit)? = null,
    icon: (@Composable () -> Unit)? = null,
    title: (@Composable () -> Unit)? = null,
    text: (@Composable () -> Unit)? = null,
    containerColor: Color = NoCap.GlassStrong,
    properties: DialogProperties = DialogProperties(),
) {
    androidx.compose.material3.AlertDialog(
        onDismissRequest = onDismissRequest,
        confirmButton = confirmButton,
        modifier = modifier,
        dismissButton = dismissButton,
        icon = icon,
        title = title,
        text = text,
        shape = RoundedCornerShape(NcTokens.Radius),
        containerColor = containerColor,
        titleContentColor = NoCap.Text,
        textContentColor = NoCap.Muted,
        iconContentColor = NoCap.Red,
        tonalElevation = androidx.compose.ui.unit.Dp(0f),
        properties = properties,
    )
}

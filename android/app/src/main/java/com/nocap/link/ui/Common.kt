package com.nocap.link.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.unit.dp

@Composable
fun SearchField(value: String, onChange: (String) -> Unit, hint: String, modifier: Modifier = Modifier) {
    Row(
        modifier.fillMaxWidth().height(48.dp).glass(RoundedCornerShape(24.dp)).padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Filled.Search, contentDescription = null, tint = NoCap.Muted, modifier = Modifier.size(20.dp))
        Box(Modifier.weight(1f).padding(horizontal = 10.dp)) {
            if (value.isEmpty()) Text(hint, style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted, maxLines = 1)
            BasicTextField(
                value, onChange, singleLine = true, modifier = Modifier.fillMaxWidth(),
                textStyle = MaterialTheme.typography.bodyLarge, cursorBrush = SolidColor(NoCap.Red),
            )
        }
        if (value.isNotEmpty()) {
            Icon(Icons.Filled.Close, contentDescription = "Clear", tint = NoCap.Muted, modifier = Modifier.size(20.dp).clickable { onChange("") })
        }
    }
}

@Composable
fun Toast(text: String, modifier: Modifier = Modifier) {
    Box(modifier.padding(horizontal = 24.dp).glass(RoundedCornerShape(50), strong = true, edge = NoCap.Red.copy(alpha = 0.5f)).padding(horizontal = 18.dp, vertical = 10.dp)) {
        Text(text, style = MaterialTheme.typography.titleSmall)
    }
}

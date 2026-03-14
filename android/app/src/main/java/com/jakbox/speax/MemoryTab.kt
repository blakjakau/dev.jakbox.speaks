package com.jakbox.speax

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.material3.MaterialTheme
import androidx.compose.foundation.background
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import java.text.NumberFormat

@Composable
fun MemoryTab(mainActivity: MainActivity) {
    val numFormat = NumberFormat.getNumberInstance()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp)
            .verticalScroll(rememberScrollState())
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            Button(
                onClick = { mainActivity.rebuildSummary() },
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.primary)
            ) { Text("Rebuild", color = Color.White) }
            
            Button(
                onClick = { mainActivity.clearHistory() },
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.buttonColors(containerColor = Color(0xFF8A3A3A))
            ) { Text("Expunge", color = Color(0xFFE0A6A6)) }
        }
        
        Spacer(Modifier.height(24.dp))

        // Memory Usage Bars
        val ctxPct = if (mainActivity.maxTokens > 0) (mainActivity.estTokens.toFloat() / mainActivity.maxTokens).coerceIn(0f, 1f) else 0f
        StatBar(
            label = "Active Context Est. (Tokens)",
            value = "${numFormat.format(mainActivity.estTokens)} / ${numFormat.format(mainActivity.maxTokens)}",
            progress = ctxPct,
            fillColor = MaterialTheme.colorScheme.primary
        )

        Spacer(Modifier.height(16.dp))

        val arcPct = if (mainActivity.maxArchiveTurns > 0) (mainActivity.archiveTurns.toFloat() / mainActivity.maxArchiveTurns).coerceIn(0f, 1f) else 0f
        StatBar(
            label = "Archive Capacity (Turns)",
            value = "${mainActivity.archiveTurns} / ${mainActivity.maxArchiveTurns}",
            progress = arcPct,
            fillColor = MaterialTheme.colorScheme.secondary
        )

        // Token Usage Breakdown
        if (mainActivity.tokenUsage.isNotEmpty()) {
            Spacer(Modifier.height(24.dp))
            Text("API Token Usage:", color = MaterialTheme.colorScheme.secondary, fontWeight = FontWeight.Bold)
            Spacer(Modifier.height(8.dp))
            Card(
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f)),
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(modifier = Modifier.padding(12.dp)) {
                    mainActivity.tokenUsage.forEach { (key, tokens) ->
                        val displayKey = if (key == "ollama" || key == "default") "Local (Ollama)" else if (key.length > 10) "Key: ...${key.takeLast(4)}" else key
                        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                            Text(displayKey, color = MaterialTheme.colorScheme.onSurface, fontSize = 14.sp)
                            Text(numFormat.format(tokens), color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        }
                        Spacer(Modifier.height(4.dp))
                    }
                }
            }
        }

        Spacer(Modifier.height(24.dp))
        Text("Summary of ${mainActivity.archiveTurns} older turns:", color = MaterialTheme.colorScheme.secondary, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(8.dp))
        Text(mainActivity.memorySummary, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.7f), lineHeight = 20.sp)
    }
}

@Composable
fun StatBar(label: String, value: String, progress: Float, fillColor: Color) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(label, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.8f))
            Text(value, fontSize = 12.sp, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.onSurface)
        }
        Spacer(Modifier.height(4.dp))
        Box(modifier = Modifier.fillMaxWidth().height(8.dp).clip(androidx.compose.foundation.shape.RoundedCornerShape(4.dp)).background(MaterialTheme.colorScheme.surfaceVariant)) {
            Box(
                modifier = Modifier
                    .fillMaxWidth(progress)
                    .fillMaxHeight()
                    .clip(androidx.compose.foundation.shape.RoundedCornerShape(4.dp))
                    .background(fillColor)
            )
        }
    }
}

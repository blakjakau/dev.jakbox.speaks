package com.jakbox.speax

import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.scale

@Composable
fun AlyxLogo(modifier: Modifier = Modifier, tint: Color = Color.White) {
    Canvas(modifier = modifier) {
        // Map the 100x100 SVG viewBox directly to whatever size the modifier requests
        scale(scaleX = size.width / 100f, scaleY = size.height / 100f, pivot = Offset.Zero) {
            val shadowColor = tint.copy(alpha = 0.35f)
            val activeColor = tint

            // Shadow / Thought Layer
            drawRoundRect(color = shadowColor, topLeft = Offset(14f, 42.65f), size = Size(8f, 8.36f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = shadowColor, topLeft = Offset(26f, 27.60f), size = Size(8f, 28.35f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = shadowColor, topLeft = Offset(38f, 17.33f), size = Size(8f, 54.05f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = shadowColor, topLeft = Offset(50f, 22.74f), size = Size(8f, 67.76f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = shadowColor, topLeft = Offset(62f, 40.14f), size = Size(8f, 45.75f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = shadowColor, topLeft = Offset(74f, 47.46f), size = Size(8f, 21.13f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = shadowColor, topLeft = Offset(86f, 48.02f), size = Size(8f, 6.40f), cornerRadius = CornerRadius(4f))

            // Active / Speech Layer
            drawRoundRect(color = activeColor, topLeft = Offset(10f, 45.00f), size = Size(8f, 13.37f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = activeColor, topLeft = Offset(22f, 44.67f), size = Size(8f, 27.57f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = activeColor, topLeft = Offset(34f, 45.99f), size = Size(8f, 32.26f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = activeColor, topLeft = Offset(46f, 38.51f), size = Size(8f, 31.38f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = activeColor, topLeft = Offset(58f, 29.07f), size = Size(8f, 26.99f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = activeColor, topLeft = Offset(70f, 29.93f), size = Size(8f, 22.98f), cornerRadius = CornerRadius(4f))
            drawRoundRect(color = activeColor, topLeft = Offset(82f, 41.02f), size = Size(8f, 12.13f), cornerRadius = CornerRadius(4f))
 
        }
    }
}

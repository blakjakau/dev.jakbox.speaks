package com.jakbox.speax

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb

val DarkColors = darkColorScheme(
    background = Color(0xFF030B17),      // Slate Deep (Depressed/Shadows)
    surface = Color(0xFF0B1E36),         // Slate Mid (Cards/Surfaces)
    surfaceVariant = Color(0xFF152E4D),  // Slate High (Active Elements/Borders)
    primary = Color(0xFF0E639C),
    secondary = Color(0xFF00D1C1),
    onBackground = Color.White,
    onSurface = Color.White,
    outline = Color(0xFF152E4D)
)

val LightColors = lightColorScheme(
    background = Color(0xFFF5F5F5),
    surface = Color(0xFFFFFFFF),
    surfaceVariant = Color(0xFFE0E0E0),
    primary = Color(0xFF0E639C),
    secondary = Color(0xFF007A5E), // Darker green for contrast on light mode
    onBackground = Color(0xFF1E1E1E),
    onSurface = Color(0xFF1E1E1E),
    outline = Color.LightGray
)

@Composable
fun SpeaxTheme(darkTheme: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    val dynamicTheme = SpeaxManager.currentTheme
    
    val colorScheme = if (dynamicTheme != null) {
        val primary = parseColor(dynamicTheme.primary)
        val secondary = parseColor(dynamicTheme.secondary)
        val tertiary = parseColor(dynamicTheme.tertiary)
        val background = parseColor(dynamicTheme.background)
        val surface = parseColor(dynamicTheme.panel)
        
        darkColorScheme(
            primary = primary,
            secondary = secondary,
            tertiary = tertiary,
            background = background,
            surface = surface,
            surfaceVariant = surface.copy(alpha = 0.7f),
            onPrimary = Color.White,
            onSecondary = Color.Black,
            onTertiary = Color.White,
            onBackground = Color.White,
            onSurface = Color.White,
            outline = surface.copy(alpha = 0.5f)
        )
    } else {
        if (darkTheme) DarkColors else LightColors
    }

    MaterialTheme(
        colorScheme = colorScheme,
        content = content
    )
}

private fun parseColor(hex: String): Color {
    return try {
        var processedHex = hex.trim()
        if (processedHex.startsWith("#") && processedHex.length == 9) {
            // Convert Web-style #RRGGBBAA to Android-style #AARRGGBB
            val rrggbb = processedHex.substring(1, 7)
            val aa = processedHex.substring(7, 9)
            processedHex = "#$aa$rrggbb"
        }
        Color(android.graphics.Color.parseColor(processedHex))
    } catch (e: Exception) {
        Color.Gray
    }
}

fun Color.desaturate(ratio: Float): Color {
    val hsv = FloatArray(3)
    android.graphics.Color.colorToHSV(this.toArgb(), hsv)
    hsv[1] *= (1f - ratio)
    return Color(android.graphics.Color.HSVToColor(hsv))
}

fun Color.mix(other: Color, ratio: Float): Color {
    return Color(
        red = this.red * (1f - ratio) + other.red * ratio,
        green = this.green * (1f - ratio) + other.green * ratio,
        blue = this.blue * (1f - ratio) + other.blue * ratio,
        alpha = this.alpha * (1f - ratio) + other.alpha * ratio
    )
}

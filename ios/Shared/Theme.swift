import SwiftUI

// MARK: - Palette
//
// Mirrors internal/server/web/styles.css so the app, widgets, and Live
// Activity look like the existing MEATER Monitor web UI.

enum Theme {
    static let bg = Color(hex: 0x0B0F17)
    static let bgSoft = Color(hex: 0x121826)
    static let card = Color(hex: 0x161D2E)
    static let card2 = Color(hex: 0x1B2336)
    static let line = Color(hex: 0x263049)
    static let text = Color(hex: 0xE8EDF7)
    static let muted = Color(hex: 0x8A96B0)
    static let accent = Color(hex: 0xFF6B3D)
    static let accent2 = Color(hex: 0xFFB03D)
    static let good = Color(hex: 0x36D399)
    static let cool = Color(hex: 0x3DA9FF)
    static let bad = Color(hex: 0xE5484D)

    static let ringGradient = AngularGradient(
        colors: [accent, accent2, accent],
        center: .center,
        startAngle: .degrees(-90),
        endAngle: .degrees(270))

    /// The web UI's page background: dark navy with warm/cool radial glows.
    static var background: some View {
        ZStack {
            bg
            RadialGradient(colors: [accent.opacity(0.12), .clear],
                           center: .init(x: 0.85, y: -0.05),
                           startRadius: 0, endRadius: 500)
            RadialGradient(colors: [cool.opacity(0.10), .clear],
                           center: .init(x: -0.05, y: 1.05),
                           startRadius: 0, endRadius: 420)
        }
        .ignoresSafeArea()
    }

    static func color(for state: CookState) -> Color {
        switch state {
        case .cooking: return accent2
        case .stalled: return cool
        case .ready: return good
        case .disconnected: return bad
        case .idle, .waiting: return muted
        }
    }
}

extension Color {
    init(hex: UInt32) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: 1)
    }
}

// MARK: - Formatting

enum Format {
    static func celsius(toDisplay c: Double, fahrenheit: Bool) -> Double {
        fahrenheit ? c * 9 / 5 + 32 : c
    }

    static func display(toCelsius v: Double, fahrenheit: Bool) -> Double {
        fahrenheit ? (v - 32) * 5 / 9 : v
    }

    /// "63.4" — one decimal, no trailing ".0" noise beyond that.
    static func temp(_ celsius: Double, fahrenheit: Bool) -> String {
        let v = Self.celsius(toDisplay: celsius, fahrenheit: fahrenheit)
        return String(format: "%.1f", (v * 10).rounded() / 10)
    }

    /// "63°" — rounded to a whole degree for compact spots.
    static func tempWhole(_ celsius: Double, fahrenheit: Bool) -> String {
        "\(Int(Self.celsius(toDisplay: celsius, fahrenheit: fahrenheit).rounded()))°"
    }

    static func unitSuffix(fahrenheit: Bool) -> String { fahrenheit ? "°F" : "°C" }

    /// Rise rate keeps two decimals so a stall (~0.04°/min) stays visible.
    static func rate(_ celsiusPerMin: Double, fahrenheit: Bool) -> String {
        let v = fahrenheit ? celsiusPerMin * 9 / 5 : celsiusPerMin
        let rounded = (v * 100).rounded() / 100
        return String(format: "%@%.2f", rounded > 0 ? "+" : "", rounded)
    }

    /// "1:04:09" over an hour, "12:34" under.
    static func duration(_ seconds: Double) -> String {
        guard seconds >= 0 else { return "--:--" }
        let total = Int(seconds.rounded())
        let h = total / 3600
        let m = (total % 3600) / 60
        let s = total % 60
        if h > 0 { return String(format: "%d:%02d:%02d", h, m, s) }
        return String(format: "%d:%02d", m, s)
    }

    /// Short wall-clock time like "19:30" in the user's locale.
    static func clock(_ date: Date) -> String {
        date.formatted(date: .omitted, time: .shortened)
    }
}

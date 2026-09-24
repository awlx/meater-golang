import SwiftUI

// MARK: - Card

/// The web UI's card: soft vertical gradient, hairline border, deep shadow.
struct CardBackground: ViewModifier {
    func body(content: Content) -> some View {
        content
            .padding(18)
            .background(
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .fill(LinearGradient(
                        colors: [Theme.card, Theme.card2],
                        startPoint: .top, endPoint: .bottom))
                    .overlay(
                        RoundedRectangle(cornerRadius: 18, style: .continuous)
                            .strokeBorder(Theme.line, lineWidth: 1))
                    .shadow(color: .black.opacity(0.45), radius: 15, y: 10))
    }
}

extension View {
    func card() -> some View { modifier(CardBackground()) }
}

// MARK: - Section header ("Current cook", "Target doneness", ...)

struct CardHeader<Trailing: View>: View {
    let title: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack {
            Text(title)
                .font(.subheadline)
                .foregroundStyle(Theme.muted)
            Spacer()
            trailing
        }
    }
}

extension CardHeader where Trailing == EmptyView {
    init(title: String) {
        self.init(title: title) { EmptyView() }
    }
}

// MARK: - Status pill

enum PillTone { case on, off, warn }

struct StatusPill: View {
    let text: String
    let tone: PillTone
    var pulses = false

    @State private var pulse = false

    private var color: Color {
        switch tone {
        case .on: return Theme.good
        case .off: return Theme.bad
        case .warn: return Theme.accent2
        }
    }

    var body: some View {
        HStack(spacing: 7) {
            Circle()
                .fill(tone == .off ? Theme.bad : color)
                .frame(width: 8, height: 8)
                .overlay {
                    if pulses && tone == .on {
                        Circle()
                            .stroke(color.opacity(pulse ? 0 : 0.5), lineWidth: 3)
                            .scaleEffect(pulse ? 2.4 : 1)
                    }
                }
            Text(text)
                .font(.footnote.weight(.medium))
                .foregroundStyle(tone == .off ? Theme.muted : color)
                .lineLimit(1)
                .fixedSize()
        }
        .padding(.horizontal, 13)
        .padding(.vertical, 7)
        .background(Capsule().fill(Theme.card))
        .overlay(Capsule().strokeBorder(Theme.line, lineWidth: 1))
        .onAppear {
            guard pulses else { return }
            withAnimation(.easeOut(duration: 2).repeatForever(autoreverses: false)) {
                pulse = true
            }
        }
    }
}

// MARK: - State badge (waiting / cooking / stalled / ready / disconnected)

struct StateBadge: View {
    let state: CookState

    var body: some View {
        let color = Theme.color(for: state)
        Text(state.rawValue.capitalized)
            .font(.footnote.weight(.bold))
            .kerning(0.4)
            .foregroundStyle(color)
            .padding(.horizontal, 16)
            .padding(.vertical, 7)
            .background(Capsule().fill(color.opacity(state == .waiting || state == .idle ? 0 : 0.09)))
            .overlay(Capsule().strokeBorder(
                state == .waiting || state == .idle ? Theme.line : color.opacity(0.42),
                lineWidth: 1))
    }
}

// MARK: - Small controls

/// Bordered secondary button matching the web UI's "cook-new" buttons.
struct SecondaryButtonStyle: ButtonStyle {
    var tint = Theme.text

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.footnote.weight(.semibold))
            .foregroundStyle(tint)
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .background(RoundedRectangle(cornerRadius: 10).fill(Theme.card2))
            .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(
                configuration.isPressed ? Theme.accent : Theme.line, lineWidth: 1))
            .opacity(configuration.isPressed ? 0.85 : 1)
    }
}

/// Filled accent button matching the web UI's "Set" button.
struct AccentButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.footnote.weight(.bold))
            .foregroundStyle(Color(hex: 0x1A0F0A))
            .padding(.horizontal, 15)
            .padding(.vertical, 8)
            .background(RoundedRectangle(cornerRadius: 10).fill(Theme.accent))
            .opacity(configuration.isPressed ? 0.8 : 1)
    }
}

/// Dark inset text-field styling like the web inputs.
struct InsetFieldStyle: ViewModifier {
    func body(content: Content) -> some View {
        content
            .padding(.horizontal, 12)
            .padding(.vertical, 9)
            .background(RoundedRectangle(cornerRadius: 10).fill(Theme.bgSoft))
            .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Theme.line, lineWidth: 1))
    }
}

extension View {
    func insetField() -> some View { modifier(InsetFieldStyle()) }
}

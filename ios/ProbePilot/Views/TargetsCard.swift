import SwiftUI

/// "Target doneness" card: preset grid plus a custom-temperature field, same
/// presets as the web UI.
struct TargetsCard: View {
    @Environment(AppModel.self) private var model

    @State private var customValue = ""
    @FocusState private var customFocused: Bool

    private let columns = [GridItem(.adaptive(minimum: 96), spacing: 10)]

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            CardHeader(title: "Target doneness") {
                HStack(spacing: 6) {
                    TextField("custom", text: $customValue)
                        .keyboardType(.decimalPad)
                        .focused($customFocused)
                        .frame(width: 76)
                        .insetField()
                    Button("Set") { submitCustom() }
                        .buttonStyle(AccentButtonStyle())
                }
            }

            LazyVGrid(columns: columns, spacing: 10) {
                ForEach(donenessPresets) { preset in
                    presetButton(preset)
                }
            }
        }
        .card()
    }

    private func presetButton(_ preset: DonenessPreset) -> some View {
        let isActive = model.status.map { abs($0.targetCelsius - preset.celsius) < 0.5 } ?? false
        return Button {
            model.setTarget(celsius: preset.celsius)
        } label: {
            VStack(spacing: 2) {
                Text(preset.name)
                    .font(.footnote.weight(.bold))
                    .foregroundStyle(Theme.text)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
                Text("\(Int(Format.celsius(toDisplay: preset.celsius, fahrenheit: model.usesFahrenheit).rounded()))\(Format.unitSuffix(fahrenheit: model.usesFahrenheit))")
                    .font(.caption)
                    .monospacedDigit()
                    .foregroundStyle(Theme.muted)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 10)
            .background(
                RoundedRectangle(cornerRadius: 12)
                    .fill(isActive ? Theme.accent.opacity(0.12) : Theme.bgSoft))
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .strokeBorder(isActive ? Theme.accent : Theme.line, lineWidth: 1))
        }
        .buttonStyle(.plain)
        .animation(.easeOut(duration: 0.15), value: isActive)
    }

    private func submitCustom() {
        guard let value = Double(customValue.replacingOccurrences(of: ",", with: ".")) else { return }
        model.setTarget(celsius: Format.display(toCelsius: value, fahrenheit: model.usesFahrenheit))
        customValue = ""
        customFocused = false
    }
}

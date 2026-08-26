import SwiftUI

/// Ambient-range and almost-done alert configuration, delivered as local
/// notifications — the app's version of the web UI's alerts card.
struct AlertsCard: View {
    @Environment(AppModel.self) private var model

    @State private var lowText = ""
    @State private var highText = ""
    @State private var etaMinutesText = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            CardHeader(title: "Ambient alerts") {
                Toggle("On", isOn: Binding(
                    get: { model.alertConfig.enabled },
                    set: { model.alertConfig.enabled = $0 }))
                    .labelsHidden()
                    .tint(Theme.accent)
            }

            HStack(spacing: 12) {
                thresholdField(label: "Below", text: $lowText)
                thresholdField(label: "Above", text: $highText)
            }

            CardHeader(title: "Almost-done alert") {
                Toggle("On", isOn: Binding(
                    get: { model.alertConfig.etaEnabled },
                    set: { model.alertConfig.etaEnabled = $0 }))
                    .labelsHidden()
                    .tint(Theme.accent)
            }

            HStack(spacing: 8) {
                Text("Notify")
                    .font(.subheadline)
                    .foregroundStyle(Theme.muted)
                TextField("30", text: $etaMinutesText)
                    .keyboardType(.numberPad)
                    .focused($focused)
                    .frame(width: 64)
                    .insetField()
                    .onSubmit(commitEtaMinutes)
                Text("min before done")
                    .font(.subheadline)
                    .foregroundStyle(Theme.muted)
            }

            notificationsButton
        }
        .card()
        .onAppear(perform: syncFields)
        .onChange(of: model.usesFahrenheit) { syncFields() }
        .onChange(of: focused) { _, isFocused in
            if !isFocused {
                commitEtaMinutes()
                syncFields()
            }
        }
    }

    private var notificationsButton: some View {
        Button {
            Task { await model.notifications.requestAuthorization() }
        } label: {
            Text(model.notifications.authorized ? "Notifications on" : "Enable notifications")
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(model.notifications.authorized ? Theme.good : Theme.text)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 10)
                .background(RoundedRectangle(cornerRadius: 10).fill(Theme.bgSoft))
                .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(
                    model.notifications.authorized ? Theme.good : Theme.line, lineWidth: 1))
        }
        .buttonStyle(.plain)
    }

    private func thresholdField(label: String, text: Binding<String>) -> some View {
        HStack(spacing: 8) {
            Text(label)
                .font(.subheadline)
                .foregroundStyle(Theme.muted)
            TextField("--", text: text)
                .keyboardType(.decimalPad)
                .focused($focused)
                .insetField()
                .onSubmit { commitThresholds() }
            Text(Format.unitSuffix(fahrenheit: model.usesFahrenheit))
                .font(.subheadline)
                .foregroundStyle(Theme.muted)
        }
    }

    private func commitThresholds() {
        if let low = Double(lowText.replacingOccurrences(of: ",", with: ".")) {
            model.alertConfig.lowCelsius = Format.display(toCelsius: low, fahrenheit: model.usesFahrenheit)
        }
        if let high = Double(highText.replacingOccurrences(of: ",", with: ".")) {
            model.alertConfig.highCelsius = Format.display(toCelsius: high, fahrenheit: model.usesFahrenheit)
        }
    }

    private func commitEtaMinutes() {
        commitThresholds()
        if let minutes = Int(etaMinutesText), minutes > 0 {
            model.alertConfig.etaMinutes = minutes
        }
    }

    private func syncFields() {
        lowText = String(Int(Format.celsius(toDisplay: model.alertConfig.lowCelsius, fahrenheit: model.usesFahrenheit).rounded()))
        highText = String(Int(Format.celsius(toDisplay: model.alertConfig.highCelsius, fahrenheit: model.usesFahrenheit).rounded()))
        etaMinutesText = String(model.alertConfig.etaMinutes)
    }
}

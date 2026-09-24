import SwiftUI

/// Server address plus app-level toggles. The server URL is stored in the
/// shared app group so widgets can refresh from the network too.
struct SettingsView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss

    @State private var urlText = ""

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("http://meater.local:8080", text: $urlText)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .font(.body.monospaced())
                } header: {
                    Text("Server address")
                } footer: {
                    Text("Base URL of your meater-golang server, e.g. http://192.168.1.20:8080. The app streams live updates from /api/stream; widgets refresh from /api/status.")
                }

                Section {
                    Toggle("Live Activity during cooks", isOn: Binding(
                        get: { model.liveActivityEnabled },
                        set: { model.liveActivityEnabled = $0 }))
                        .tint(Theme.accent)
                } footer: {
                    Text("Shows the cook on the Lock Screen and in the Dynamic Island while a session is running. Updates stream in while the app is open; afterwards the Live Activity keeps the last reading and marks it stale.")
                }

                Section {
                    LabeledContent("Connection", value: connectionText)
                    if let error = model.lastError {
                        Text(error)
                            .font(.footnote)
                            .foregroundStyle(Theme.bad)
                    }
                } header: {
                    Text("Status")
                }
            }
            .scrollContentBackground(.hidden)
            .background(Theme.bg)
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        model.serverURLString = urlText.trimmingCharacters(in: .whitespaces)
                        dismiss()
                    }
                    .fontWeight(.bold)
                }
            }
        }
        .preferredColorScheme(.dark)
        .onAppear { urlText = model.serverURLString }
    }

    private var connectionText: String {
        switch model.phase {
        case .configuring: return "No server configured"
        case .connecting: return "Connecting…"
        case .live: return "Live"
        case .reconnecting: return "Reconnecting…"
        }
    }
}

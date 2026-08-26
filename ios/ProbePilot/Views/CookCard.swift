import SwiftUI

/// "Current cook" card: name + meat type fields, Save, and the Start/Stop
/// session toggle — same behaviour as the web UI.
struct CookCard: View {
    @Environment(AppModel.self) private var model

    @State private var name = ""
    @State private var meatType = ""
    @FocusState private var focusedField: Field?

    private enum Field { case name, meat }

    private var isRunning: Bool { model.status?.running ?? false }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            CardHeader(title: "Current cook") {
                Button(isRunning ? "Stop" : "Start") {
                    if isRunning {
                        model.stopSession()
                    } else {
                        model.startSession(name: name, meatType: meatType)
                    }
                }
                .buttonStyle(SecondaryButtonStyle(tint: isRunning ? Theme.bad : Theme.good))
            }

            TextField("What are you cooking?", text: $name)
                .focused($focusedField, equals: .name)
                .insetField()

            TextField("Meat type (e.g. pork neck)", text: $meatType)
                .focused($focusedField, equals: .meat)
                .autocorrectionDisabled()
                .insetField()

            // Suggestions from past cooks, so the ETA history matching is one
            // tap away (the web UI's <datalist>).
            if focusedField == .meat && !model.knownMeatTypes.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(model.knownMeatTypes, id: \.self) { type in
                            Button(type) {
                                meatType = type
                                focusedField = nil
                            }
                            .buttonStyle(SecondaryButtonStyle())
                        }
                    }
                }
            }

            Button("Save") {
                model.saveCookInfo(name: name, meatType: meatType)
                focusedField = nil
            }
            .buttonStyle(SecondaryButtonStyle())
        }
        .card()
        .onChange(of: model.status?.cookName) { _, new in
            // Reflect the server value without clobbering active typing.
            if focusedField != .name, let new { name = new }
        }
        .onChange(of: model.status?.meatType) { _, new in
            if focusedField != .meat, let new { meatType = new }
        }
        .onAppear {
            name = model.status?.cookName ?? ""
            meatType = model.status?.meatType ?? ""
        }
    }
}

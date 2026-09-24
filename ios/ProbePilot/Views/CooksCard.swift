import SwiftUI

/// "Past cooks" card: saved cook history with tap-to-view on the chart and
/// swipe-free explicit delete, mirroring the web list.
struct CooksCard: View {
    @Environment(AppModel.self) private var model

    @State private var cookPendingDelete: CookMeta?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            CardHeader(title: "Past cooks") {
                Button("Refresh") {
                    Task { await model.reloadCooks() }
                }
                .buttonStyle(SecondaryButtonStyle())
            }

            if model.cooks.isEmpty {
                Text("No saved cooks yet.")
                    .font(.footnote)
                    .foregroundStyle(Theme.muted)
                    .padding(.vertical, 4)
            } else {
                VStack(spacing: 8) {
                    ForEach(model.cooks) { cook in
                        cookRow(cook)
                    }
                }
            }
        }
        .card()
        .confirmationDialog(
            "Delete \"\(cookPendingDelete?.displayName ?? "")\"? This can't be undone.",
            isPresented: Binding(
                get: { cookPendingDelete != nil },
                set: { if !$0 { cookPendingDelete = nil } }),
            titleVisibility: .visible
        ) {
            Button("Delete cook", role: .destructive) {
                if let cook = cookPendingDelete { model.deleteCook(cook) }
                cookPendingDelete = nil
            }
        }
    }

    private func cookRow(_ cook: CookMeta) -> some View {
        let isViewing = model.viewingCook?.id == cook.id
        // The row is a tap target with a nested delete Button; using
        // onTapGesture (not an outer Button) keeps the two taps independent.
        return HStack(spacing: 10) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(cook.displayName)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                    Text(metaLine(cook))
                        .font(.caption)
                        .foregroundStyle(Theme.muted)
                        .lineLimit(1)
                }
                Spacer(minLength: 8)
                Text("max \(Format.tempWhole(cook.maxTipCelsius, fahrenheit: model.usesFahrenheit))\(model.usesFahrenheit ? "F" : "C")")
                    .font(.caption)
                    .monospacedDigit()
                    .foregroundStyle(Theme.muted)
                if cook.active {
                    Text("LIVE")
                        .font(.caption2.weight(.heavy))
                        .kerning(0.5)
                        .foregroundStyle(Theme.good)
                        .padding(.horizontal, 9)
                        .padding(.vertical, 3)
                        .background(Capsule().fill(Theme.good.opacity(0.16)))
                } else {
                    Button {
                        cookPendingDelete = cook
                    } label: {
                        Image(systemName: "xmark")
                            .font(.caption2.weight(.bold))
                            .foregroundStyle(Theme.muted)
                            .frame(width: 24, height: 24)
                            .background(Circle().strokeBorder(Theme.line))
                    }
                    .buttonStyle(.plain)
                }
            }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .background(
            RoundedRectangle(cornerRadius: 12)
                .fill(isViewing ? Theme.accent.opacity(0.08) : Theme.card2))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .strokeBorder(isViewing ? Theme.accent : Theme.line, lineWidth: 1))
        .contentShape(RoundedRectangle(cornerRadius: 12))
        .onTapGesture { model.viewCook(cook) }
    }

    private func metaLine(_ cook: CookMeta) -> String {
        var parts = [
            cook.startedAt.formatted(.dateTime.month(.abbreviated).day())
                + " " + Format.clock(cook.startedAt),
        ]
        if let ended = cook.endedAt {
            parts.append(Format.duration(ended.timeIntervalSince(cook.startedAt)))
        }
        let meat = cook.meatType.trimmingCharacters(in: .whitespaces)
        if !meat.isEmpty { parts.append(meat) }
        return parts.joined(separator: " · ")
    }
}

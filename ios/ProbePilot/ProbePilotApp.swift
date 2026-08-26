import SwiftUI

@main
struct ProbePilotApp: App {
    @State private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            DashboardView()
                .environment(model)
                .preferredColorScheme(.dark)
                .tint(Theme.accent)
        }
    }
}

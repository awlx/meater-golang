import SwiftUI
import WidgetKit

@main
struct ProbePilotWidgetsBundle: WidgetBundle {
    var body: some Widget {
        CookStatusWidget()
        CookLiveActivity()
    }
}

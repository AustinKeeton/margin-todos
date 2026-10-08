// margin-todos UI, loaded into reMarkable's writing screen (DocumentView.qml) by margin-todos.qmd.
// A tab near the bottom-right corner opens a scrollable list of this notebook's to-dos over the
// bottom third of the screen; the tab rides up on top of the panel and closes it again.
// Reads /home/root/todo/todos.json (written by the detector) and keeps check marks in
// /home/root/todo/state.json. Tap with a finger: the pen draws on the page, not on this UI.
import QtQuick 2.15
import com.remarkable

Item {
    id: root
    anchors.fill: parent

    readonly property string dir: "file:///home/root/todo/"
    readonly property int cm: 89                    // 1 cm at the rM2's 226 dpi
    readonly property var doc: documentView.document  // DocumentView's open notebook
    property var allTodos: []
    property var state: ({})
    property bool open: false

    // This notebook's to-dos: open ones first, then completed; newest first within each.
    readonly property var todos: {
        const id = doc ? doc.id : ""
        const mine = allTodos.filter(t => t.doc === id)
        // Ties (to-dos first seen in the same scan) fall back to drawing order: later box
        // strokes have higher ids ("<doc>-<page>-<author>.<counter>").
        const counter = t => parseInt(t.id.split(".").pop()) || 0
        const newest = (a, b) => ((b.created || 0) - (a.created || 0)) || (counter(b) - counter(a))
        return mine.filter(t => !isChecked(t)).sort(newest)
            .concat(mine.filter(t => isChecked(t)).sort(newest))
    }
    readonly property int openCount: todos.filter(t => !isChecked(t)).length

    function isChecked(t) {
        return state[t.id] !== undefined ? state[t.id] : t.checkedInInk
    }

    function readJson(name, done) {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE) return
            try { done(JSON.parse(xhr.responseText)) } catch (e) { done(null) }
        }
        xhr.open("GET", dir + name)
        xhr.send()
    }

    function reload() {
        readJson("todos.json", d => { if (d && d.todos) root.allTodos = d.todos })
        readJson("state.json", d => { root.state = d || {} })
    }

    function toggle(t) {
        const s = Object.assign({}, state)
        s[t.id] = !isChecked(t)
        state = s
        const xhr = new XMLHttpRequest()
        xhr.open("PUT", dir + "state.json")
        xhr.send(JSON.stringify(s, null, 2))
    }

    // Opens the to-do's page and scrolls so its line sits in the upper part of the view.
    function goTo(t) {
        if (!doc) return
        const page = doc.pageForId(t.page)
        if (page < 0) return
        try {
            // A scroll position is the page y at the bottom of the view.
            LibraryController.setScrollPosition(doc.id, page, t.y + Math.round(root.height * 0.6))
            documentView.openPage(page, 1) // DocumentView.ScrollPosition.Restore
        } catch (e) {
            documentView.openPage(page)
        }
        open = false
    }

    Component.onCompleted: reload()
    onDocChanged: { open = false; reload() }

    // Picks up new to-dos from the detector; quick while the list is open, slow otherwise.
    Timer {
        interval: root.open ? 2000 : 10000
        repeat: true
        running: true
        onTriggered: root.reload()
    }

    // Drawn marks: the tablet's font has no check or arrow glyphs.
    component Check: Item {
        width: 32; height: 32
        Rectangle { x: 3; y: 17; width: 13; height: 5; radius: 2; color: "black"; rotation: 45; antialiasing: true }
        Rectangle { x: 9; y: 13; width: 24; height: 5; radius: 2; color: "black"; rotation: -50; antialiasing: true }
    }
    component Chevron: Item {
        property bool up: true
        width: 22; height: 14
        Rectangle { x: 0; y: 5; width: 14; height: 4; radius: 2; color: "black"; antialiasing: true; rotation: up ? -40 : 40 }
        Rectangle { x: 8; y: 5; width: 14; height: 4; radius: 2; color: "black"; antialiasing: true; rotation: up ? 40 : -40 }
    }

    // The panel: bottom third of the screen.
    Rectangle {
        id: panel
        visible: root.open
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        height: Math.round(parent.height / 3)
        color: "white"

        Rectangle { id: topEdge; anchors.left: parent.left; anchors.right: parent.right; anchors.top: parent.top; height: 3; color: "black" }

        // Swallows taps on empty panel space so they don't reach the page UI below.
        MouseArea { anchors.fill: parent }

        ListView {
            id: list
            anchors.fill: parent
            anchors.topMargin: 3
            clip: true
            model: root.todos
            boundsBehavior: Flickable.StopAtBounds

            delegate: Item {
                width: list.width
                height: 104
                readonly property var todo: modelData
                readonly property bool done: root.isChecked(todo)

                // The row opens the to-do's place in the notebook...
                MouseArea { anchors.fill: parent; onClicked: root.goTo(todo) }

                // ...and the checkbox completes it.
                Item {
                    id: boxArea
                    width: 112
                    anchors.left: parent.left
                    anchors.top: parent.top
                    anchors.bottom: parent.bottom
                    Rectangle {
                        anchors.centerIn: parent
                        width: 48; height: 48
                        border.color: "black"
                        border.width: 3
                        color: "white"
                        Check { anchors.centerIn: parent; visible: done }
                    }
                    MouseArea { anchors.fill: parent; onClicked: root.toggle(todo) }
                }
                Image {
                    anchors.left: boxArea.right
                    anchors.right: parent.right
                    anchors.rightMargin: 32
                    anchors.top: parent.top
                    anchors.topMargin: 8
                    height: 72
                    fillMode: Image.PreserveAspectFit
                    horizontalAlignment: Image.AlignLeft
                    source: todo.image ? root.dir + todo.image : ""
                    opacity: done ? 0.35 : 1
                    cache: false
                }
                Text {
                    anchors.left: boxArea.right
                    anchors.bottom: parent.bottom
                    anchors.bottomMargin: 4
                    text: "page " + (root.doc ? root.doc.pageForId(todo.page) + 1 : todo.pageNumber)
                    font.pixelSize: 18
                    color: "#555555"
                }
                Rectangle { anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom; height: 1; color: "#999999" }
            }

            Text {
                anchors.centerIn: parent
                visible: root.todos.length === 0
                text: "No to-dos in this notebook yet.\nDraw a box in the left margin, then write to its right."
                horizontalAlignment: Text.AlignHCenter
                font.pixelSize: 22
                color: "#555555"
            }
        }
    }

    // The tab, 1 cm from the right edge. Closed, it sits on the bottom edge; open, it rides on
    // top of the panel, joined to it, and closes it.
    Rectangle {
        id: tab
        width: 230
        height: 54
        radius: 10
        z: 2
        anchors.right: parent.right
        anchors.rightMargin: root.cm
        anchors.bottom: root.open ? panel.top : parent.bottom
        anchors.bottomMargin: -12 // tuck the rounded bottom corners out of sight
        color: "white"
        border.color: "black"
        border.width: 3

        Row {
            anchors.centerIn: parent
            anchors.verticalCenterOffset: -6
            spacing: 14
            Text {
                text: root.openCount > 0 ? "To-dos · " + root.openCount : "To-dos"
                font.pixelSize: 24
                color: "black"
                anchors.verticalCenter: parent.verticalCenter
            }
            Chevron { up: !root.open; anchors.verticalCenter: parent.verticalCenter }
        }
        MouseArea {
            anchors.fill: parent
            anchors.margins: -12 // a bigger target than it looks
            onClicked: { root.reload(); root.open = !root.open }
        }
    }
    // Joins an open tab to the panel: hides the tab's tucked bottom and the panel's top edge
    // under it, so the two read as one shape.
    Rectangle {
        visible: root.open
        z: 3
        x: tab.x + 3
        width: tab.width - 6
        y: panel.y - 3
        height: 18
        color: "white"
    }
}

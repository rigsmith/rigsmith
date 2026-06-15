// Prevent an extra console window on Windows in release builds.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

// Minimal Tauri app: open the window declared in tauri.conf.json and nothing
// else. The demo is about the *release* (shiprig versions tauri.conf.json +
// Cargo.toml and builds the installer), not the app.
fn main() {
    tauri::Builder::default()
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

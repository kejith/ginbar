fn main() {
    // SVT-AV1 static library requires pthread and math system libraries.
    // The svt-av1-enc crate's build.rs handles the library search path
    // but doesn't always emit the transitive system deps.
    println!("cargo:rustc-link-lib=pthread");
    println!("cargo:rustc-link-lib=m");
}

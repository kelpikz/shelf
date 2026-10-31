//! Runs the built shelf binary on the cases in tests/cmd. Each <case>.trycmd
//! holds the commands and the expected output; <case>.in/ is the folder they
//! run in. To update the expected output: TRYCMD=overwrite cargo test --test cli_tests

#[test]
fn cli_tests() {
    trycmd::TestCases::new().case("tests/cmd/**/*.trycmd");
}

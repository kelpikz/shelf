// Formats the CLI code as XXXX-XXXX while the Owner types. The server accepts
// the code with or without the dash, so the page works without this script.
const input = document.getElementById("code");
if (input) {
  input.addEventListener("input", () => {
    const raw = input.value.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 8);
    input.value = raw.length > 4 ? raw.slice(0, 4) + "-" + raw.slice(4) : raw;
  });
}

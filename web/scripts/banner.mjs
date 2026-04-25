// Boxland — terminal banner.
//
// Printed by `just design` (and `just serve`) right before the Go
// server starts listening, so non-developers see a friendly,
// clickable entry point instead of a wall of slog output.
//
// Uses cfonts (https://github.com/dominikwilkowski/cfonts) for the
// rainbow ASCII title. Falls back to a plain banner when the terminal
// doesn't support the gradient (e.g. some CI runners).

import cfonts from "cfonts";

const env = process.env;

// HTTP listen address; matches BOXLAND_HTTP_ADDR's default.
const httpAddr = env.BOXLAND_HTTP_ADDR ?? ":8080";

// Convert ":8080" -> "http://localhost:8080" for the user.
function publicURL(addr) {
	if (addr.startsWith(":")) return `http://localhost${addr}`;
	if (addr.startsWith("0.0.0.0")) return `http://localhost${addr.slice(7)}`;
	if (!addr.startsWith("http")) return `http://${addr}`;
	return addr;
}

const baseURL = publicURL(httpAddr);

cfonts.say("Boxland", {
	font: "block",
	align: "left",
	colors: ["candy"],            // built-in rainbow gradient palette
	background: "transparent",
	letterSpacing: 1,
	lineHeight: 1,
	space: false,
	maxLength: "0",
	gradient: ["red", "magenta"], // fallback if 'candy' isn't supported
	independentGradient: false,
	transitionGradient: true,
});

const rows = [
	["Design tools", `${baseURL}/design/login`],
	["Player game",  `${baseURL}/play/login`],
	["Health check", `${baseURL}/healthz`],
];

const labelWidth = Math.max(...rows.map((r) => r[0].length));
for (const [label, url] of rows) {
	const pad = " ".repeat(labelWidth - label.length);
	process.stdout.write(`  ${label}${pad}  →  \x1b[36m${url}\x1b[0m\n`);
}
process.stdout.write("\n");

if (env.BOXLAND_ENV === "dev" || !env.BOXLAND_ENV) {
	process.stdout.write(
		"  \x1b[2mFirst time? Sign up as a designer at /design/signup,\n" +
		"  or as a player at /play/signup. Logs follow:\x1b[0m\n\n",
	);
}

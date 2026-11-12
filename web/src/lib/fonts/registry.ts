// Self-hosted typefaces keep the embedded console independent of font CDNs.
import localFont from "next/font/local";

const instrumentSans = localFont({
  src: "./brand/InstrumentSans-Variable.ttf",
  weight: "400 700",
  variable: "--font-instrument-sans",
  display: "swap",
});
const geistMono = localFont({
  src: [
    { path: "./brand/GeistMono-Regular.woff2", weight: "400" },
    { path: "./brand/GeistMono-Medium.woff2", weight: "500" },
  ],
  variable: "--font-geist-mono",
  display: "swap",
});

export const fontVars = `${instrumentSans.variable} ${geistMono.variable}`;

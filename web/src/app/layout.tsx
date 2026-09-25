import type { Metadata, Viewport } from "next";
import "../styles/tokens.css";
import "../styles/fonts.css";
import "../styles/app.css";

export const metadata: Metadata = {
  title: "رادار بازار",
  description: "داشبورد لحظه‌ای بازار سرمایه",
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#0B1016" },
    { media: "(prefers-color-scheme: light)", color: "#F6F7F9" },
  ],
};

// Applies a saved manual theme before the first paint (no flash); "auto" follows the system.
const themeScript = `try{var t=localStorage.getItem("theme");if(t==="light"||t==="dark")document.documentElement.setAttribute("data-theme",t)}catch(e){}`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="fa" dir="rtl" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeScript }} />
      </head>
      <body>{children}</body>
    </html>
  );
}

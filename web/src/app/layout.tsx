import type { Metadata, Viewport } from "next";
import "../styles/tokens.css";
import "../styles/fonts.css";
import "../styles/app.css";

export const metadata: Metadata = {
  title: "رادار بازار",
  description: "داشبورد لحظه‌ای بازار سرمایه",
};

export const viewport: Viewport = { width: "device-width", initialScale: 1, themeColor: "#0B1016" };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="fa" dir="rtl">
      <body>{children}</body>
    </html>
  );
}

import type { Metadata } from "next";
import { Geist, Geist_Mono, Inter, Roboto_Slab } from "next/font/google";
import "./globals.css";
import { ClientLayout } from "./client-layout";
import { cn } from "@/lib/utils";

const inter = Inter({subsets:['latin'],variable:'--font-sans'});
const robotoSlab = Roboto_Slab({subsets:['latin'],variable:'--font-roboto-slab'});

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "The Wolf — Multi Tool Code Analysis Engine",
  description:
    "Multi-tool static analysis orchestrator and automated fix engine by AlphaBravo",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" suppressHydrationWarning className={cn("font-sans", inter.variable, robotoSlab.variable)}>
      <body
        className={`${geistSans.variable} ${geistMono.variable} antialiased`}
      >
        <ClientLayout>{children}</ClientLayout>
      </body>
    </html>
  );
}

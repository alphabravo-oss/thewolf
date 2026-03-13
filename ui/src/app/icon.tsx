import { ImageResponse } from "next/og";
import { WOLF_PATH } from "@/components/wolf-logo";

export const size = { width: 32, height: 32 };
export const contentType = "image/png";

export default function Icon() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: "white",
          borderRadius: "6px",
        }}
      >
        <svg
          viewBox="0 0 640 640"
          width="24"
          height="24"
          style={{ fill: "#18181b" }}
        >
          <path d={WOLF_PATH} />
        </svg>
      </div>
    ),
    { ...size }
  );
}

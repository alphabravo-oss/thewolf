// WolfLogo — inline SVG wolf-head silhouette. Same path as
// /wolf.svg + /public/icon.svg so the favicon, sidebar, and
// auth pages all read as the same logo.
//
// Color treatment is consistent across both themes: black wolf
// inside a white rounded-square card. The white card gives the
// mark presence against the dark sidebar AND any light-mode
// background — one variant to maintain, recognisable anywhere.
// Single source of truth for the silhouette, shared by both variants.
const WOLF_PATH =
  "M 279 118 L 270 125 L 263 141 L 251 183 L 245 215 L 243 251 L 257 292 L 218 344 L 222 345 L 241 341 L 199 415 L 226 405 L 228 406 L 219 429 L 218 455 L 221 466 L 236 454 L 238 455 L 234 469 L 234 489 L 237 504 L 245 522 L 249 526 L 251 499 L 257 485 L 263 478 L 260 496 L 260 518 L 265 535 L 274 551 L 285 561 L 280 548 L 280 531 L 348 605 L 390 646 L 378 626 L 363 592 L 322 485 L 309 474 L 246 432 L 257 377 L 289 335 L 296 330 L 330 324 L 353 338 L 327 294 L 327 290 L 359 316 L 371 346 L 363 399 L 359 451 L 380 443 L 401 443 L 412 446 L 420 451 L 422 450 L 410 345 L 421 316 L 454 287 L 455 289 L 435 320 L 426 339 L 448 324 L 486 331 L 523 378 L 534 432 L 459 484 L 458 492 L 415 600 L 393 645 L 398 642 L 502 531 L 503 543 L 498 561 L 508 551 L 517 535 L 522 514 L 518 478 L 526 488 L 533 507 L 532 525 L 534 524 L 541 511 L 547 488 L 547 470 L 542 454 L 544 453 L 559 465 L 562 455 L 561 431 L 551 405 L 553 404 L 578 415 L 581 414 L 540 341 L 562 344 L 520 290 L 532 255 L 532 226 L 518 159 L 508 130 L 501 120 L 491 118 L 484 122 L 431 197 L 429 194 L 422 194 L 398 200 L 389 183 L 381 200 L 356 194 L 347 194 L 345 197 L 290 121 Z M 490 161 L 494 163 L 497 172 L 497 193 L 491 207 L 478 218 L 486 217 L 498 210 L 499 218 L 494 234 L 480 257 L 507 247 L 507 252 L 498 275 L 444 234 L 445 228 L 480 172 Z M 286 161 L 307 187 L 332 226 L 335 234 L 282 274 L 272 248 L 291 255 L 300 256 L 287 237 L 279 217 L 280 210 L 291 217 L 299 218 L 285 208 L 280 196 L 280 170 Z M 366 456 L 361 466 L 368 476 L 369 484 L 375 492 L 386 497 L 393 497 L 403 493 L 410 486 L 412 475 L 419 467 L 418 460 L 414 456 L 405 453 L 381 452 Z M 360 511 L 375 523 L 385 526 L 397 526 L 414 519 L 422 509 L 389 503 L 361 509 Z M 303 339 L 322 362 L 340 364 L 354 381 L 355 363 L 336 341 Z M 478 340 L 443 341 L 425 363 L 426 381 L 440 363 L 457 363 Z M 453 466 L 444 472 L 438 494 L 428 508 L 439 503 L 445 496 Z M 328 466 L 335 497 L 351 508 L 342 493 L 338 474 Z";

export function WolfLogo({
  className = "size-7",
  bare = false,
}: {
  className?: string;
  /**
   * Render only the silhouette, in `currentColor`, with no white card. Use
   * when the caller already supplies a container — the sidebar seats the mark
   * in a rounded gradient tile, and a nested white card inside it would read
   * as two stacked badges.
   */
  bare?: boolean;
}) {
  if (bare) {
    return (
      <svg
        viewBox="100 92 580 580"
        className={className}
        fill="currentColor"
        xmlns="http://www.w3.org/2000/svg"
        role="img"
        aria-label="The Wolf"
      >
        <path fillRule="evenodd" d={WOLF_PATH} />
      </svg>
    );
  }
  return (
    <div
      className={
        "inline-flex items-center justify-center rounded-md bg-white p-0.5 " +
        className
      }
    >
      <svg
        // Tighter viewBox crops to the wolf's bounding box plus a small
        // margin (~5%) so the silhouette fills the white card instead
        // of floating in 20% of empty space. Original SVG path lives at
        // roughly 199..581 × 118..646 of a 768 viewBox; this crops to a
        // 580x580 square centered on the wolf.
        viewBox="100 92 580 580"
        className="size-full text-black"
        fill="currentColor"
        xmlns="http://www.w3.org/2000/svg"
        role="img"
        aria-label="The Wolf"
      >
        <path fillRule="evenodd" d={WOLF_PATH} />
      </svg>
    </div>
  );
}

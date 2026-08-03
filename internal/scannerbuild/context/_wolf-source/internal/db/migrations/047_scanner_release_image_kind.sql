-- Separate scanner runtime images from fixer execution images while retaining
-- both in one immutable, signed release inventory. Existing rows predate
-- fixer publication and are therefore scanner images.
ALTER TABLE scanner_release_images
    ADD COLUMN image_kind TEXT NOT NULL DEFAULT 'scanner'
    CHECK (image_kind IN ('scanner', 'fixer'));

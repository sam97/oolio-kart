"use client";
import React from "react";
import Image from "next/image";

interface ImageContainer {
  className?: string;
  src: string;
  width?: number;
  height?: number;
  alt: string;
  onClickFunc?: () => void;
}

export default function ImageContainer(props: ImageContainer) {
  const {
    className = "object-contain",
    src = "",
    width = 15,
    height = 15,
    alt = "",
    onClickFunc,
  } = props;

  return (
    <div className="overflow-hidden" onClick={onClickFunc}>
      <Image
        className={className}
        src={src}
        priority
        width={width}
        height={height}
        alt={alt}
        quality={100}
      />
    </div>
  );
}

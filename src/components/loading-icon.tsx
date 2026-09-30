"use client";
import React from "react";
import { Rings } from "react-loader-spinner";

interface ILoadingIcon {
  color: string;
  size: string;
}

export const LoadingIcon = (props: ILoadingIcon) => {
  const { color = "#C73B0E", size = "50" } = props;
  return (
    <Rings
      height={size}
      width={size}
      color={color}
      radius="6"
      wrapperStyle={{}}
      wrapperClass="loading-icon"
      visible={true}
      ariaLabel="rings-loading"
    />
  );
};

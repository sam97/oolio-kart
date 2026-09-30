"use client";
import { LoadingIcon } from "./loading-icon";

interface ICustomButton {
  buttonText: string;
  customStyle?: string;
  onClickFunc?: Function;
  disabled?: boolean;
  fullWidth?: boolean;
  isLoading?: boolean;
}

export const CustomButton = (props: ICustomButton) => {
  const {
    buttonText = "",
    customStyle = "",
    onClickFunc,
    disabled = false,
    fullWidth = true,
    isLoading = false,
  } = props;

  const action = () => {
    if (onClickFunc) return onClickFunc();
    return null;
  };

  const defaultStyle = `border-0 bg-primary text-xs tracking-wider ${
    fullWidth && "w-full"
  }`;

  return (
    <button
      onClick={() => action()}
      disabled={disabled}
      className={
        customStyle
          ? `${customStyle} ${defaultStyle} ${
              disabled && "cursor-not-allowed"
            } `
          : `${defaultStyle}`
      }
    >
      {isLoading && <LoadingIcon size={35} />}
      {!isLoading && buttonText}
    </button>
  );
};

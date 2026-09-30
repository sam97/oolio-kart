"use client";
import React from "react";
import ImageContainer from "../image";
import { createCartStore, IProduct } from "@/stores/cart";

interface IQtyCounter {
  product: IProduct;
}

export default function QtyCounter(props: IQtyCounter) {
  const { product } = props;
  const { carts, incrementQty, decrementQty } = createCartStore();

  return (
    <div className="flex justify-around items-center min-w-[150px]">
      {/* Minus Icon */}
      <ImageContainer
        className="cursor-pointer"
        src="/assets/minus.png"
        alt="minus-icon"
        onClickFunc={() => decrementQty(product.id)}
      />
      {/* Quantity */}
      <div>
        <p className="txt text-[#EAB096]">{carts[product.id].qty}</p>
      </div>
      {/* Plus Icon */}
      <ImageContainer
        className="cursor-pointer"
        src="/assets/plus.png"
        alt="plus-icon"
        onClickFunc={() => incrementQty(product.id)}
      />
    </div>
  );
}

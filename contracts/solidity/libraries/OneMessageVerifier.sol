
// SPDX-License-Identifier: MIT

pragma solidity ^0.8.0;

/// @title Groth16 verifier template.
/// @author Remco Bloemen
/// @notice Supports verifying Groth16 proofs. Proofs can be in uncompressed
/// (256 bytes) and compressed (128 bytes) format. A view function is provided
/// to compress proofs.
/// @notice See <https://2π.com/23/bn254-compression> for further explanation.
library OneMessageVerifier {

    /// Some of the provided public input values are larger than the field modulus.
    /// @dev Public input elements are not automatically reduced, as this is can be
    /// a dangerous source of bugs.
    error PublicInputNotInField();

    /// The proof is invalid.
    /// @dev This can mean that provided Groth16 proof points are not on their
    /// curves, that pairing equation fails, or that the proof is not for the
    /// provided public input.
    error ProofInvalid();
    /// The commitment is invalid
    /// @dev This can mean that provided commitment points and/or proof of knowledge are not on their
    /// curves, that pairing equation fails, or that the commitment and/or proof of knowledge is not for the
    /// commitment key.
    error CommitmentInvalid();

    // Addresses of precompiles
    uint256 constant PRECOMPILE_MODEXP = 0x05;
    uint256 constant PRECOMPILE_ADD = 0x06;
    uint256 constant PRECOMPILE_MUL = 0x07;
    uint256 constant PRECOMPILE_VERIFY = 0x08;

    // Base field Fp order P and scalar field Fr order R.
    // For BN254 these are computed as follows:
    //     t = 4965661367192848881
    //     P = 36⋅t⁴ + 36⋅t³ + 24⋅t² + 6⋅t + 1
    //     R = 36⋅t⁴ + 36⋅t³ + 18⋅t² + 6⋅t + 1
    uint256 constant P = 0x30644e72e131a029b85045b68181585d97816a916871ca8d3c208c16d87cfd47;
    uint256 constant R = 0x30644e72e131a029b85045b68181585d2833e84879b9709143e1f593f0000001;

    // Extension field Fp2 = Fp[i] / (i² + 1)
    // Note: This is the complex extension field of Fp with i² = -1.
    //       Values in Fp2 are represented as a pair of Fp elements (a₀, a₁) as a₀ + a₁⋅i.
    // Note: The order of Fp2 elements is *opposite* that of the pairing contract, which
    //       expects Fp2 elements in order (a₁, a₀). This is also the order in which
    //       Fp2 elements are encoded in the public interface as this became convention.

    // Constants in Fp
    uint256 constant FRACTION_1_2_FP = 0x183227397098d014dc2822db40c0ac2ecbc0b548b438e5469e10460b6c3e7ea4;
    uint256 constant FRACTION_27_82_FP = 0x2b149d40ceb8aaae81be18991be06ac3b5b4c5e559dbefa33267e6dc24a138e5;
    uint256 constant FRACTION_3_82_FP = 0x2fcd3ac2a640a154eb23960892a85a68f031ca0c8344b23a577dcf1052b9e775;

    // Exponents for inversions and square roots mod P
    uint256 constant EXP_INVERSE_FP = 0x30644E72E131A029B85045B68181585D97816A916871CA8D3C208C16D87CFD45; // P - 2
    uint256 constant EXP_SQRT_FP = 0xC19139CB84C680A6E14116DA060561765E05AA45A1C72A34F082305B61F3F52; // (P + 1) / 4;

    // Groth16 alpha point in G1
    uint256 constant ALPHA_X = 8646570538863986852361212142579408592394177512511644279431918188622563542392;
    uint256 constant ALPHA_Y = 11415085704032412371980843837895073547170662358513080362072015021457422326058;

    // Groth16 beta point in G2 in powers of i
    uint256 constant BETA_NEG_X_0 = 17696236637225154867587537997702590428701818927647496751655025348644912788272;
    uint256 constant BETA_NEG_X_1 = 16463942809220574014784837074425213197684826292735477330062625924416460226532;
    uint256 constant BETA_NEG_Y_0 = 15651448768437385202268738531469840411005108854171245236531278182437703980923;
    uint256 constant BETA_NEG_Y_1 = 18003308886847770527051115575191579221440772791162024013618224962511694025492;

    // Groth16 gamma point in G2 in powers of i
    uint256 constant GAMMA_NEG_X_0 = 10079632175882801048217180556839204315059032687918217475619606307939575166653;
    uint256 constant GAMMA_NEG_X_1 = 15154003017284439085845404704799008990934889562183672992834619021508418475557;
    uint256 constant GAMMA_NEG_Y_0 = 18375977598220254203225644242567385038689661994543525832367097184792899769852;
    uint256 constant GAMMA_NEG_Y_1 = 20050625653315154404327180590721190347553952073515439373881469175750610326713;

    // Groth16 delta point in G2 in powers of i
    uint256 constant DELTA_NEG_X_0 = 10926673653030803847098571147822324220461910725901021908031528462379519362954;
    uint256 constant DELTA_NEG_X_1 = 5193627672294870702449362446113748870140883921122661789828726292507474496399;
    uint256 constant DELTA_NEG_Y_0 = 14325153525329308881017374205401277647001010578301368500487490610900580101840;
    uint256 constant DELTA_NEG_Y_1 = 13870954937936235516619810083031772899862739014964886736891655740550564737695;
    // Pedersen G point in G2 in powers of i
    uint256 constant PEDERSEN_G_X_0 = 18070182408736705733904298802491832048673550834404919880714748745338264614802;
    uint256 constant PEDERSEN_G_X_1 = 16025524288739026258276430774011361991052851531221293033739291912124518134931;
    uint256 constant PEDERSEN_G_Y_0 = 4934310080351404506606062578262956141658152043862640830048632293806103522232;
    uint256 constant PEDERSEN_G_Y_1 = 7621617873955040819081307950862592387662114821258467046169367633920146450277;

    // Pedersen GSigma point in G2 in powers of i
    uint256 constant PEDERSEN_GSIGMA_X_0 = 17940258963004436171754259526871766546245916430920284892922028291235331062225;
    uint256 constant PEDERSEN_GSIGMA_X_1 = 21361091670094542078947720950327896967418142161167411435696642108361357172627;
    uint256 constant PEDERSEN_GSIGMA_Y_0 = 19261612055436687416499451221315837246150671408645292966873322715470875689521;
    uint256 constant PEDERSEN_GSIGMA_Y_1 = 6148487309475068467747591841143787302515530727658921993892776141863470825525;

    // Constant and public input points
    uint256 constant CONSTANT_X = 10801328057994073943766740839402335855073553608766404198290958320617651480295;
    uint256 constant CONSTANT_Y = 10452715462055323708904334110134682642762575720453882696710176276355151461165;
    uint256 constant PUB_0_X = 20284965962303730559343787789315726899776877786978307550365983844861321462170;
    uint256 constant PUB_0_Y = 17430796849860685862589523737224660749289835908002969650002457334010191928975;
    uint256 constant PUB_1_X = 9092634501862128788738274239770444698895424418391831001541895155423141433509;
    uint256 constant PUB_1_Y = 14821104452509110797671636399966540988334134320989732028548148034405799107001;
    uint256 constant PUB_2_X = 11751013598599851511709286837322961802153874210689508767054237048220870151180;
    uint256 constant PUB_2_Y = 12521035556398366790964115603169837767177056967874040942608444655337912549046;
    uint256 constant PUB_3_X = 17738563045136080011303900883982015763991865766723197146680776090403850305597;
    uint256 constant PUB_3_Y = 8977903051481557161225570260920586286731525256289511560991068983291988091756;
    uint256 constant PUB_4_X = 19238886287290885978710982499974854032096643791737546147751052627639374987406;
    uint256 constant PUB_4_Y = 11513925034541212692971092641404424034400813238089811726151456750952297811330;
    uint256 constant PUB_5_X = 17762802825357502872814798339600491592596281318539536826436301868472685291410;
    uint256 constant PUB_5_Y = 14361998723810851986178285974726889795407423336171883661712750388622359371791;
    uint256 constant PUB_6_X = 17742097315859247794266597536458979093024201857382486422338402026428701319507;
    uint256 constant PUB_6_Y = 5735276734945672325575190136553989129713208347642794839581068315588751782746;
    uint256 constant PUB_7_X = 17626704075237899805179949054374715433931858636665632217898233097607214583227;
    uint256 constant PUB_7_Y = 14541886818377633676497287331856942720936327433233823556462685305680532748920;
    uint256 constant PUB_8_X = 18453986695032286888712437479906235198802484293926944341725565887849176203731;
    uint256 constant PUB_8_Y = 12337755540904552532165805429117313637689137546494591100480504086885367716709;
    uint256 constant PUB_9_X = 7184722311294785639568227776195063921216559353554049834251499969673338446361;
    uint256 constant PUB_9_Y = 8453513541699155397313256982299863397095295719764296539581877181038539027356;
    uint256 constant PUB_10_X = 20751230243301929653396659951567231642297763443626300264439885837351869019351;
    uint256 constant PUB_10_Y = 17953060936219313623738941447200574272315505283707369788348273201761486836100;
    uint256 constant PUB_11_X = 16852116374537728691438570549560522195529044305098031417349620540204951897671;
    uint256 constant PUB_11_Y = 11948966118227199428373409384369224372040344336190835210358115266768427258068;
    uint256 constant PUB_12_X = 17434900903582233117800259340606980926719885604047451843039054771728674078841;
    uint256 constant PUB_12_Y = 8626437313639717972546140597442733956027872072879162140614452980558702743565;
    uint256 constant PUB_13_X = 1305958629200699027661575501339765534259322560900716255486476832954770287710;
    uint256 constant PUB_13_Y = 12074012255802841505419667452576453189047073122274690265464815390277745779212;
    uint256 constant PUB_14_X = 13587600594505928715745192260849459628343221019882005158580719685725893041175;
    uint256 constant PUB_14_Y = 19618078044194853445019817632194091857111376848884170214799214521883591087106;
    uint256 constant PUB_15_X = 1122940304032411746330648781411814619854041785356836115465252203372724974715;
    uint256 constant PUB_15_Y = 12128419734876881471556389707866661085671110234536485001249517241246401904923;
    uint256 constant PUB_16_X = 15101440881847802255558257305366249271455127343052431974468452393648133704976;
    uint256 constant PUB_16_Y = 20759710722604436793101045874908793683754397381090130541529438469663911524015;
    uint256 constant PUB_17_X = 20173256814526065188049230892702398631098691666613077397045347996707120057397;
    uint256 constant PUB_17_Y = 20164886478219118728927506205810316747029478155880820644103248520419531679462;
    uint256 constant PUB_18_X = 3036258331721831172094646624862461822828102151524282539237771933418092464908;
    uint256 constant PUB_18_Y = 16776206594036472729260519363276456219798038566724296067282172409618744369141;
    uint256 constant PUB_19_X = 13708725497065896637401231762952067683424237489481790726420324717369200451336;
    uint256 constant PUB_19_Y = 427379465121743096676538940906132481813644146337529987453418665018170520850;
    uint256 constant PUB_20_X = 19919981455329268331496594532099872743914006771130098131348126375112673864146;
    uint256 constant PUB_20_Y = 14873223773215927794686221204793706338854865505033349482555777731338173407726;
    uint256 constant PUB_21_X = 21750408499962064905744412795665892816737587150312320847371704762847086202076;
    uint256 constant PUB_21_Y = 11667871979691711406541007268657738940523584753896568871802049259619748534659;
    uint256 constant PUB_22_X = 3640051274377317414208229644094803132339521773071846189657976786795312898435;
    uint256 constant PUB_22_Y = 21316316377768104144790435816865633755466105691573582246610663629169410162740;
    uint256 constant PUB_23_X = 15986361173943117238561887339630592862478188688018913973201269274273406934116;
    uint256 constant PUB_23_Y = 17058425572717973908255899650973448029910354646443393960370387904677475454619;
    uint256 constant PUB_24_X = 561420287365642195215596426407559588271191655450666466298779084001160260387;
    uint256 constant PUB_24_Y = 8807302674564174957717300920021852474401833532109590690445587317057073819076;
    uint256 constant PUB_25_X = 1232975809240288652757900700770425192249214382819745933184322299198460501395;
    uint256 constant PUB_25_Y = 6579955724469239896224450754582415758756999708353933467850566105686383882374;
    uint256 constant PUB_26_X = 15508656886336503620288979840693116217527357008278340005590419681445839902766;
    uint256 constant PUB_26_Y = 8395978707662855258828104773731919547699311273014911549125013448134824983683;
    uint256 constant PUB_27_X = 10984817490004613821653504619173464395613441732358687849567547360276636114460;
    uint256 constant PUB_27_Y = 20953756518544748245240601087484657653911647611746970622896226840804491711676;
    uint256 constant PUB_28_X = 2382864304732391340559204184187708359508478340769177540160991083238824860321;
    uint256 constant PUB_28_Y = 7322758842449515048865288121473118548134066373085048786620358108518476876195;
    uint256 constant PUB_29_X = 18706284426254728708738081584923287005072556641670947697195693364506030381935;
    uint256 constant PUB_29_Y = 16700796504410234757458950633735839492310052931914441302660972221709105060104;
    uint256 constant PUB_30_X = 10709321829366761814690778133963869353556635746386101628713720576613429374217;
    uint256 constant PUB_30_Y = 11149226544748630900901145686445380798848966972186675641839519034862567392403;
    uint256 constant PUB_31_X = 11822889530137429488466590733285410201391330832478097600857951373721324071509;
    uint256 constant PUB_31_Y = 725399466631687789295924953651762033375749963674719675442986258040776462493;
    uint256 constant PUB_32_X = 7241327300399909458968429425062555024668127609884996070103723947005015410546;
    uint256 constant PUB_32_Y = 14362678562359594825238571470984565555835242631903539708322044932349740966401;

    /// Negation in Fp.
    /// @notice Returns a number x such that a + x = 0 in Fp.
    /// @notice The input does not need to be reduced.
    /// @param a the base
    /// @return x the result
    function negate(uint256 a) internal pure returns (uint256 x) {
        unchecked {
            x = (P - (a % P)) % P; // Modulo is cheaper than branching
        }
    }

    /// Exponentiation in Fp.
    /// @notice Returns a number x such that a ^ e = x in Fp.
    /// @notice The input does not need to be reduced.
    /// @param a the base
    /// @param e the exponent
    /// @return x the result
    function exp(uint256 a, uint256 e) internal view returns (uint256 x) {
        bool success;
        assembly ("memory-safe") {
            let f := mload(0x40)
            mstore(f, 0x20)
            mstore(add(f, 0x20), 0x20)
            mstore(add(f, 0x40), 0x20)
            mstore(add(f, 0x60), a)
            mstore(add(f, 0x80), e)
            mstore(add(f, 0xa0), P)
            success := staticcall(gas(), PRECOMPILE_MODEXP, f, 0xc0, f, 0x20)
            x := mload(f)
        }
        if (!success) {
            // Exponentiation failed.
            // Should not happen.
            revert ProofInvalid();
        }
    }

    /// Invertsion in Fp.
    /// @notice Returns a number x such that a * x = 1 in Fp.
    /// @notice The input does not need to be reduced.
    /// @notice Reverts with ProofInvalid() if the inverse does not exist
    /// @param a the input
    /// @return x the solution
    function invert_Fp(uint256 a) internal view returns (uint256 x) {
        x = exp(a, EXP_INVERSE_FP);
        if (mulmod(a, x, P) != 1) {
            // Inverse does not exist.
            // Can only happen during G2 point decompression.
            revert ProofInvalid();
        }
    }

    /// Square root in Fp.
    /// @notice Returns a number x such that x * x = a in Fp.
    /// @notice Will revert with InvalidProof() if the input is not a square
    /// or not reduced.
    /// @param a the square
    /// @return x the solution
    function sqrt_Fp(uint256 a) internal view returns (uint256 x) {
        x = exp(a, EXP_SQRT_FP);
        if (mulmod(x, x, P) != a) {
            // Square root does not exist or a is not reduced.
            // Happens when G1 point is not on curve.
            revert ProofInvalid();
        }
    }

    /// Square test in Fp.
    /// @notice Returns whether a number x exists such that x * x = a in Fp.
    /// @notice Will revert with InvalidProof() if the input is not a square
    /// or not reduced.
    /// @param a the square
    /// @return x the solution
    function isSquare_Fp(uint256 a) internal view returns (bool) {
        uint256 x = exp(a, EXP_SQRT_FP);
        return mulmod(x, x, P) == a;
    }

    /// Square root in Fp2.
    /// @notice Fp2 is the complex extension Fp[i]/(i^2 + 1). The input is
    /// a0 + a1 ⋅ i and the result is x0 + x1 ⋅ i.
    /// @notice Will revert with InvalidProof() if
    ///   * the input is not a square,
    ///   * the hint is incorrect, or
    ///   * the input coefficents are not reduced.
    /// @param a0 The real part of the input.
    /// @param a1 The imaginary part of the input.
    /// @param hint A hint which of two possible signs to pick in the equation.
    /// @return x0 The real part of the square root.
    /// @return x1 The imaginary part of the square root.
    function sqrt_Fp2(uint256 a0, uint256 a1, bool hint) internal view returns (uint256 x0, uint256 x1) {
        // If this square root reverts there is no solution in Fp2.
        uint256 d = sqrt_Fp(addmod(mulmod(a0, a0, P), mulmod(a1, a1, P), P));
        if (hint) {
            d = negate(d);
        }
        // If this square root reverts there is no solution in Fp2.
        x0 = sqrt_Fp(mulmod(addmod(a0, d, P), FRACTION_1_2_FP, P));
        x1 = mulmod(a1, invert_Fp(mulmod(x0, 2, P)), P);

        // Check result to make sure we found a root.
        // Note: this also fails if a0 or a1 is not reduced.
        if (a0 != addmod(mulmod(x0, x0, P), negate(mulmod(x1, x1, P)), P)
        ||  a1 != mulmod(2, mulmod(x0, x1, P), P)) {
            revert ProofInvalid();
        }
    }

    /// Compress a G1 point.
    /// @notice Reverts with InvalidProof if the coordinates are not reduced
    /// or if the point is not on the curve.
    /// @notice The point at infinity is encoded as (0,0) and compressed to 0.
    /// @param x The X coordinate in Fp.
    /// @param y The Y coordinate in Fp.
    /// @return c The compresed point (x with one signal bit).
    function compress_g1(uint256 x, uint256 y) internal view returns (uint256 c) {
        if (x >= P || y >= P) {
            // G1 point not in field.
            revert ProofInvalid();
        }
        if (x == 0 && y == 0) {
            // Point at infinity
            return 0;
        }

        // Note: sqrt_Fp reverts if there is no solution, i.e. the x coordinate is invalid.
        uint256 y_pos = sqrt_Fp(addmod(mulmod(mulmod(x, x, P), x, P), 3, P));
        if (y == y_pos) {
            return (x << 1) | 0;
        } else if (y == negate(y_pos)) {
            return (x << 1) | 1;
        } else {
            // G1 point not on curve.
            revert ProofInvalid();
        }
    }

    /// Decompress a G1 point.
    /// @notice Reverts with InvalidProof if the input does not represent a valid point.
    /// @notice The point at infinity is encoded as (0,0) and compressed to 0.
    /// @param c The compresed point (x with one signal bit).
    /// @return x The X coordinate in Fp.
    /// @return y The Y coordinate in Fp.
    function decompress_g1(uint256 c) internal view returns (uint256 x, uint256 y) {
        // Note that X = 0 is not on the curve since 0³ + 3 = 3 is not a square.
        // so we can use it to represent the point at infinity.
        if (c == 0) {
            // Point at infinity as encoded in EIP196 and EIP197.
            return (0, 0);
        }
        bool negate_point = c & 1 == 1;
        x = c >> 1;
        if (x >= P) {
            // G1 x coordinate not in field.
            revert ProofInvalid();
        }

        // Note: (x³ + 3) is irreducible in Fp, so it can not be zero and therefore
        //       y can not be zero.
        // Note: sqrt_Fp reverts if there is no solution, i.e. the point is not on the curve.
        y = sqrt_Fp(addmod(mulmod(mulmod(x, x, P), x, P), 3, P));
        if (negate_point) {
            y = negate(y);
        }
    }

    /// Compress a G2 point.
    /// @notice Reverts with InvalidProof if the coefficients are not reduced
    /// or if the point is not on the curve.
    /// @notice The G2 curve is defined over the complex extension Fp[i]/(i^2 + 1)
    /// with coordinates (x0 + x1 ⋅ i, y0 + y1 ⋅ i).
    /// @notice The point at infinity is encoded as (0,0,0,0) and compressed to (0,0).
    /// @param x0 The real part of the X coordinate.
    /// @param x1 The imaginary poart of the X coordinate.
    /// @param y0 The real part of the Y coordinate.
    /// @param y1 The imaginary part of the Y coordinate.
    /// @return c0 The first half of the compresed point (x0 with two signal bits).
    /// @return c1 The second half of the compressed point (x1 unmodified).
    function compress_g2(uint256 x0, uint256 x1, uint256 y0, uint256 y1)
    internal view returns (uint256 c0, uint256 c1) {
        if (x0 >= P || x1 >= P || y0 >= P || y1 >= P) {
            // G2 point not in field.
            revert ProofInvalid();
        }
        if ((x0 | x1 | y0 | y1) == 0) {
            // Point at infinity
            return (0, 0);
        }

        // Compute y^2
        // Note: shadowing variables and scoping to avoid stack-to-deep.
        uint256 y0_pos;
        uint256 y1_pos;
        {
            uint256 n3ab = mulmod(mulmod(x0, x1, P), P-3, P);
            uint256 a_3 = mulmod(mulmod(x0, x0, P), x0, P);
            uint256 b_3 = mulmod(mulmod(x1, x1, P), x1, P);
            y0_pos = addmod(FRACTION_27_82_FP, addmod(a_3, mulmod(n3ab, x1, P), P), P);
            y1_pos = negate(addmod(FRACTION_3_82_FP,  addmod(b_3, mulmod(n3ab, x0, P), P), P));
        }

        // Determine hint bit
        // If this sqrt fails the x coordinate is not on the curve.
        bool hint;
        {
            uint256 d = sqrt_Fp(addmod(mulmod(y0_pos, y0_pos, P), mulmod(y1_pos, y1_pos, P), P));
            hint = !isSquare_Fp(mulmod(addmod(y0_pos, d, P), FRACTION_1_2_FP, P));
        }

        // Recover y
        (y0_pos, y1_pos) = sqrt_Fp2(y0_pos, y1_pos, hint);
        if (y0 == y0_pos && y1 == y1_pos) {
            c0 = (x0 << 2) | (hint ? 2  : 0) | 0;
            c1 = x1;
        } else if (y0 == negate(y0_pos) && y1 == negate(y1_pos)) {
            c0 = (x0 << 2) | (hint ? 2  : 0) | 1;
            c1 = x1;
        } else {
            // G1 point not on curve.
            revert ProofInvalid();
        }
    }

    /// Decompress a G2 point.
    /// @notice Reverts with InvalidProof if the input does not represent a valid point.
    /// @notice The G2 curve is defined over the complex extension Fp[i]/(i^2 + 1)
    /// with coordinates (x0 + x1 ⋅ i, y0 + y1 ⋅ i).
    /// @notice The point at infinity is encoded as (0,0,0,0) and compressed to (0,0).
    /// @param c0 The first half of the compresed point (x0 with two signal bits).
    /// @param c1 The second half of the compressed point (x1 unmodified).
    /// @return x0 The real part of the X coordinate.
    /// @return x1 The imaginary poart of the X coordinate.
    /// @return y0 The real part of the Y coordinate.
    /// @return y1 The imaginary part of the Y coordinate.
    function decompress_g2(uint256 c0, uint256 c1)
    internal view returns (uint256 x0, uint256 x1, uint256 y0, uint256 y1) {
        // Note that X = (0, 0) is not on the curve since 0³ + 3/(9 + i) is not a square.
        // so we can use it to represent the point at infinity.
        if (c0 == 0 && c1 == 0) {
            // Point at infinity as encoded in EIP197.
            return (0, 0, 0, 0);
        }
        bool negate_point = c0 & 1 == 1;
        bool hint = c0 & 2 == 2;
        x0 = c0 >> 2;
        x1 = c1;
        if (x0 >= P || x1 >= P) {
            // G2 x0 or x1 coefficient not in field.
            revert ProofInvalid();
        }

        uint256 n3ab = mulmod(mulmod(x0, x1, P), P-3, P);
        uint256 a_3 = mulmod(mulmod(x0, x0, P), x0, P);
        uint256 b_3 = mulmod(mulmod(x1, x1, P), x1, P);

        y0 = addmod(FRACTION_27_82_FP, addmod(a_3, mulmod(n3ab, x1, P), P), P);
        y1 = negate(addmod(FRACTION_3_82_FP,  addmod(b_3, mulmod(n3ab, x0, P), P), P));

        // Note: sqrt_Fp2 reverts if there is no solution, i.e. the point is not on the curve.
        // Note: (X³ + 3/(9 + i)) is irreducible in Fp2, so y can not be zero.
        //       But y0 or y1 may still independently be zero.
        (y0, y1) = sqrt_Fp2(y0, y1, hint);
        if (negate_point) {
            y0 = negate(y0);
            y1 = negate(y1);
        }
    }

    /// Compute the public input linear combination.
    /// @notice Reverts with PublicInputNotInField if the input is not in the field.
    /// @notice Computes the multi-scalar-multiplication of the public input
    /// elements and the verification key including the constant term.
    /// @param input The public inputs. These are elements of the scalar field Fr.
    /// @param publicCommitments public inputs generated from pedersen commitments.
    /// @param commitments The Pedersen commitments from the proof.
    /// @return x The X coordinate of the resulting G1 point.
    /// @return y The Y coordinate of the resulting G1 point.
    function publicInputMSM(
        uint256[32] calldata input,
        uint256[1] memory publicCommitments,
        uint256[2] memory commitments
    )
    internal view returns (uint256 x, uint256 y) {
        // Note: The ECMUL precompile does not reject unreduced values, so we check this.
        // Note: Unrolling this loop does not cost much extra in code-size, the bulk of the
        //       code-size is in the PUB_ constants.
        // ECMUL has input (x, y, scalar) and output (x', y').
        // ECADD has input (x1, y1, x2, y2) and output (x', y').
        // We reduce commitments(if any) with constants as the first point argument to ECADD.
        // We call them such that ecmul output is already in the second point
        // argument to ECADD so we can have a tight loop.
        bool success = true;
        assembly ("memory-safe") {
            let f := mload(0x40)
            let g := add(f, 0x40)
            let s
            mstore(f, CONSTANT_X)
            mstore(add(f, 0x20), CONSTANT_Y)
            success := and(success,  staticcall(gas(), PRECOMPILE_ADD, commitments, 64, g, 0x40))
            success := and(success,  staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_0_X)
            mstore(add(g, 0x20), PUB_0_Y)
            s :=  calldataload(input)
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_1_X)
            mstore(add(g, 0x20), PUB_1_Y)
            s :=  calldataload(add(input, 32))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_2_X)
            mstore(add(g, 0x20), PUB_2_Y)
            s :=  calldataload(add(input, 64))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_3_X)
            mstore(add(g, 0x20), PUB_3_Y)
            s :=  calldataload(add(input, 96))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_4_X)
            mstore(add(g, 0x20), PUB_4_Y)
            s :=  calldataload(add(input, 128))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_5_X)
            mstore(add(g, 0x20), PUB_5_Y)
            s :=  calldataload(add(input, 160))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_6_X)
            mstore(add(g, 0x20), PUB_6_Y)
            s :=  calldataload(add(input, 192))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_7_X)
            mstore(add(g, 0x20), PUB_7_Y)
            s :=  calldataload(add(input, 224))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_8_X)
            mstore(add(g, 0x20), PUB_8_Y)
            s :=  calldataload(add(input, 256))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_9_X)
            mstore(add(g, 0x20), PUB_9_Y)
            s :=  calldataload(add(input, 288))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_10_X)
            mstore(add(g, 0x20), PUB_10_Y)
            s :=  calldataload(add(input, 320))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_11_X)
            mstore(add(g, 0x20), PUB_11_Y)
            s :=  calldataload(add(input, 352))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_12_X)
            mstore(add(g, 0x20), PUB_12_Y)
            s :=  calldataload(add(input, 384))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_13_X)
            mstore(add(g, 0x20), PUB_13_Y)
            s :=  calldataload(add(input, 416))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_14_X)
            mstore(add(g, 0x20), PUB_14_Y)
            s :=  calldataload(add(input, 448))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_15_X)
            mstore(add(g, 0x20), PUB_15_Y)
            s :=  calldataload(add(input, 480))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_16_X)
            mstore(add(g, 0x20), PUB_16_Y)
            s :=  calldataload(add(input, 512))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_17_X)
            mstore(add(g, 0x20), PUB_17_Y)
            s :=  calldataload(add(input, 544))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_18_X)
            mstore(add(g, 0x20), PUB_18_Y)
            s :=  calldataload(add(input, 576))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_19_X)
            mstore(add(g, 0x20), PUB_19_Y)
            s :=  calldataload(add(input, 608))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_20_X)
            mstore(add(g, 0x20), PUB_20_Y)
            s :=  calldataload(add(input, 640))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_21_X)
            mstore(add(g, 0x20), PUB_21_Y)
            s :=  calldataload(add(input, 672))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_22_X)
            mstore(add(g, 0x20), PUB_22_Y)
            s :=  calldataload(add(input, 704))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_23_X)
            mstore(add(g, 0x20), PUB_23_Y)
            s :=  calldataload(add(input, 736))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_24_X)
            mstore(add(g, 0x20), PUB_24_Y)
            s :=  calldataload(add(input, 768))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_25_X)
            mstore(add(g, 0x20), PUB_25_Y)
            s :=  calldataload(add(input, 800))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_26_X)
            mstore(add(g, 0x20), PUB_26_Y)
            s :=  calldataload(add(input, 832))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_27_X)
            mstore(add(g, 0x20), PUB_27_Y)
            s :=  calldataload(add(input, 864))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_28_X)
            mstore(add(g, 0x20), PUB_28_Y)
            s :=  calldataload(add(input, 896))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_29_X)
            mstore(add(g, 0x20), PUB_29_Y)
            s :=  calldataload(add(input, 928))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_30_X)
            mstore(add(g, 0x20), PUB_30_Y)
            s :=  calldataload(add(input, 960))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_31_X)
            mstore(add(g, 0x20), PUB_31_Y)
            s :=  calldataload(add(input, 992))
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))
            mstore(g, PUB_32_X)
            mstore(add(g, 0x20), PUB_32_Y)
            s := mload(publicCommitments)
            mstore(add(g, 0x40), s)
            success := and(success, lt(s, R))
            success := and(success, staticcall(gas(), PRECOMPILE_MUL, g, 0x60, g, 0x40))
            success := and(success, staticcall(gas(), PRECOMPILE_ADD, f, 0x80, f, 0x40))

            x := mload(f)
            y := mload(add(f, 0x20))
        }
        if (!success) {
            // Either Public input not in field, or verification key invalid.
            // We assume the contract is correctly generated, so the verification key is valid.
            revert PublicInputNotInField();
        }
    }

    /// Compress a proof.
    /// @notice Will revert with InvalidProof if the curve points are invalid,
    /// but does not verify the proof itself.
    /// @param proof The uncompressed Groth16 proof. Elements are in the same order as for
    /// verifyProof. I.e. Groth16 points (A, B, C) encoded as in EIP-197.
    /// @param commitments Pedersen commitments from the proof.
    /// @param commitmentPok proof of knowledge for the Pedersen commitments.
    /// @return compressed The compressed proof. Elements are in the same order as for
    /// verifyCompressedProof. I.e. points (A, B, C) in compressed format.
    /// @return compressedCommitments compressed Pedersen commitments from the proof.
    /// @return compressedCommitmentPok compressed proof of knowledge for the Pedersen commitments.
    function compressProof(
        uint256[8] calldata proof,
        uint256[2] calldata commitments,
        uint256[2] calldata commitmentPok
    )
    public view returns (
        uint256[4] memory compressed,
        uint256[1] memory compressedCommitments,
        uint256 compressedCommitmentPok
    ) {
        compressed[0] = compress_g1(proof[0], proof[1]);
        (compressed[2], compressed[1]) = compress_g2(proof[3], proof[2], proof[5], proof[4]);
        compressed[3] = compress_g1(proof[6], proof[7]);
        compressedCommitments[0] = compress_g1(commitments[0], commitments[1]);
        compressedCommitmentPok = compress_g1(commitmentPok[0], commitmentPok[1]);
    }

    /// Verify a Groth16 proof with compressed points.
    /// @notice Reverts with InvalidProof if the proof is invalid or
    /// with PublicInputNotInField the public input is not reduced.
    /// @notice There is no return value. If the function does not revert, the
    /// proof was successfully verified.
    /// @param compressedProof the points (A, B, C) in compressed format
    /// matching the output of compressProof.
    /// @param compressedCommitments compressed Pedersen commitments from the proof.
    /// @param compressedCommitmentPok compressed proof of knowledge for the Pedersen commitments.
    /// @param input the public input field elements in the scalar field Fr.
    /// Elements must be reduced.
    function verifyCompressedProof(
        uint256[4] calldata compressedProof,
        uint256[1] calldata compressedCommitments,
        uint256 compressedCommitmentPok,
        uint256[32] calldata input
    ) public view {
        uint256[1] memory publicCommitments;
        uint256[2] memory commitments;
        uint256[24] memory pairings;
        {
            (commitments[0], commitments[1]) = decompress_g1(compressedCommitments[0]);
            (uint256 Px, uint256 Py) = decompress_g1(compressedCommitmentPok);

            uint256[] memory publicAndCommitmentCommitted;

            publicCommitments[0] = uint256(
                sha256(
                    abi.encodePacked(
                        commitments[0],
                        commitments[1],
                        publicAndCommitmentCommitted
                    )
                )
            ) % R;
            // Commitments
            pairings[ 0] = commitments[0];
            pairings[ 1] = commitments[1];
            pairings[ 2] = PEDERSEN_GSIGMA_X_1;
            pairings[ 3] = PEDERSEN_GSIGMA_X_0;
            pairings[ 4] = PEDERSEN_GSIGMA_Y_1;
            pairings[ 5] = PEDERSEN_GSIGMA_Y_0;
            pairings[ 6] = Px;
            pairings[ 7] = Py;
            pairings[ 8] = PEDERSEN_G_X_1;
            pairings[ 9] = PEDERSEN_G_X_0;
            pairings[10] = PEDERSEN_G_Y_1;
            pairings[11] = PEDERSEN_G_Y_0;

            // Verify pedersen commitments
            bool success;
            assembly ("memory-safe") {
                let f := mload(0x40)

                success := staticcall(gas(), PRECOMPILE_VERIFY, pairings, 0x180, f, 0x20)
                success := and(success, mload(f))
            }
            if (!success) {
                revert CommitmentInvalid();
            }
        }

        {
            (uint256 Ax, uint256 Ay) = decompress_g1(compressedProof[0]);
            (uint256 Bx0, uint256 Bx1, uint256 By0, uint256 By1) = decompress_g2(compressedProof[2], compressedProof[1]);
            (uint256 Cx, uint256 Cy) = decompress_g1(compressedProof[3]);
            (uint256 Lx, uint256 Ly) = publicInputMSM(
                input,
                publicCommitments,
                commitments
            );

            // Verify the pairing
            // Note: The precompile expects the F2 coefficients in big-endian order.
            // Note: The pairing precompile rejects unreduced values, so we won't check that here.
            // e(A, B)
            pairings[ 0] = Ax;
            pairings[ 1] = Ay;
            pairings[ 2] = Bx1;
            pairings[ 3] = Bx0;
            pairings[ 4] = By1;
            pairings[ 5] = By0;
            // e(C, -δ)
            pairings[ 6] = Cx;
            pairings[ 7] = Cy;
            pairings[ 8] = DELTA_NEG_X_1;
            pairings[ 9] = DELTA_NEG_X_0;
            pairings[10] = DELTA_NEG_Y_1;
            pairings[11] = DELTA_NEG_Y_0;
            // e(α, -β)
            pairings[12] = ALPHA_X;
            pairings[13] = ALPHA_Y;
            pairings[14] = BETA_NEG_X_1;
            pairings[15] = BETA_NEG_X_0;
            pairings[16] = BETA_NEG_Y_1;
            pairings[17] = BETA_NEG_Y_0;
            // e(L_pub, -γ)
            pairings[18] = Lx;
            pairings[19] = Ly;
            pairings[20] = GAMMA_NEG_X_1;
            pairings[21] = GAMMA_NEG_X_0;
            pairings[22] = GAMMA_NEG_Y_1;
            pairings[23] = GAMMA_NEG_Y_0;

            // Check pairing equation.
            bool success;
            uint256[1] memory output;
            assembly ("memory-safe") {
                success := staticcall(gas(), PRECOMPILE_VERIFY, pairings, 0x300, output, 0x20)
            }
            if (!success || output[0] != 1) {
                // Either proof or verification key invalid.
                // We assume the contract is correctly generated, so the verification key is valid.
                revert ProofInvalid();
            }
        }
    }

    /// Verify an uncompressed Groth16 proof.
    /// @notice Reverts with InvalidProof if the proof is invalid or
    /// with PublicInputNotInField the public input is not reduced.
    /// @notice There is no return value. If the function does not revert, the
    /// proof was successfully verified.
    /// @param proof the points (A, B, C) in EIP-197 format matching the output
    /// of compressProof.
    /// @param commitments the Pedersen commitments from the proof.
    /// @param commitmentPok the proof of knowledge for the Pedersen commitments.
    /// @param input the public input field elements in the scalar field Fr.
    /// Elements must be reduced.
    function verifyProof(
        uint256[8] calldata proof,
        uint256[2] calldata commitments,
        uint256[2] calldata commitmentPok,
        uint256[32] calldata input
    ) public view {
        // HashToField
        uint256[1] memory publicCommitments;
        uint256[] memory publicAndCommitmentCommitted;

            publicCommitments[0] = uint256(
                sha256(
                    abi.encodePacked(
                        commitments[0],
                        commitments[1],
                        publicAndCommitmentCommitted
                    )
                )
            ) % R;

        // Verify pedersen commitments
        bool success;
        assembly ("memory-safe") {
            let f := mload(0x40)

            calldatacopy(f, commitments, 0x40) // Copy Commitments
            mstore(add(f, 0x40), PEDERSEN_GSIGMA_X_1)
            mstore(add(f, 0x60), PEDERSEN_GSIGMA_X_0)
            mstore(add(f, 0x80), PEDERSEN_GSIGMA_Y_1)
            mstore(add(f, 0xa0), PEDERSEN_GSIGMA_Y_0)
            calldatacopy(add(f, 0xc0), commitmentPok, 0x40)
            mstore(add(f, 0x100), PEDERSEN_G_X_1)
            mstore(add(f, 0x120), PEDERSEN_G_X_0)
            mstore(add(f, 0x140), PEDERSEN_G_Y_1)
            mstore(add(f, 0x160), PEDERSEN_G_Y_0)

            success := staticcall(gas(), PRECOMPILE_VERIFY, f, 0x180, f, 0x20)
            success := and(success, mload(f))
        }
        if (!success) {
            revert CommitmentInvalid();
        }

        (uint256 x, uint256 y) = publicInputMSM(
            input,
            publicCommitments,
            commitments
        );

        // Note: The precompile expects the F2 coefficients in big-endian order.
        // Note: The pairing precompile rejects unreduced values, so we won't check that here.
        assembly ("memory-safe") {
            let f := mload(0x40) // Free memory pointer.

            // Copy points (A, B, C) to memory. They are already in correct encoding.
            // This is pairing e(A, B) and G1 of e(C, -δ).
            calldatacopy(f, proof, 0x100)

            // Complete e(C, -δ) and write e(α, -β), e(L_pub, -γ) to memory.
            // OPT: This could be better done using a single codecopy, but
            //      Solidity (unlike standalone Yul) doesn't provide a way to
            //      to do this.
            mstore(add(f, 0x100), DELTA_NEG_X_1)
            mstore(add(f, 0x120), DELTA_NEG_X_0)
            mstore(add(f, 0x140), DELTA_NEG_Y_1)
            mstore(add(f, 0x160), DELTA_NEG_Y_0)
            mstore(add(f, 0x180), ALPHA_X)
            mstore(add(f, 0x1a0), ALPHA_Y)
            mstore(add(f, 0x1c0), BETA_NEG_X_1)
            mstore(add(f, 0x1e0), BETA_NEG_X_0)
            mstore(add(f, 0x200), BETA_NEG_Y_1)
            mstore(add(f, 0x220), BETA_NEG_Y_0)
            mstore(add(f, 0x240), x)
            mstore(add(f, 0x260), y)
            mstore(add(f, 0x280), GAMMA_NEG_X_1)
            mstore(add(f, 0x2a0), GAMMA_NEG_X_0)
            mstore(add(f, 0x2c0), GAMMA_NEG_Y_1)
            mstore(add(f, 0x2e0), GAMMA_NEG_Y_0)

            // Check pairing equation.
            success := staticcall(gas(), PRECOMPILE_VERIFY, f, 0x300, f, 0x20)
            // Also check returned value (both are either 1 or 0).
            success := and(success, mload(f))
        }
        if (!success) {
            // Either proof or verification key invalid.
            // We assume the contract is correctly generated, so the verification key is valid.
            revert ProofInvalid();
        }
    }
}
